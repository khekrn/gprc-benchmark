package com.beam.bench

import io.vertx.core.Vertx
import io.vertx.kotlin.coroutines.coAwait
import io.vertx.pgclient.PgBuilder
import io.vertx.pgclient.PgConnectOptions
import io.vertx.sqlclient.Pool
import io.vertx.sqlclient.PoolOptions
import io.vertx.sqlclient.SqlConnection
import io.vertx.sqlclient.Tuple

/**
 * Wraps the shared Vert.x reactive Postgres pool.
 *
 * The pool is built once on the *root* Vertx (see [MainVerticle]) and shared
 * across all [GrpcVerticle] instances. vertx-pg-client multiplexes connections
 * across event loops internally; there's no benefit to one pool per verticle
 * and a real cost (more idle connections, less pipelining headroom).
 *
 * All query methods here are suspending and stay on the calling event loop —
 * pg-client never blocks, so we don't switch dispatchers.
 */
class Db(private val pool: Pool) {

    /** Result of [getState]. `found == false` means no row for the workflow. */
    data class StateRow(
        val found: Boolean,
        val workflowId: String,
        val state: String,
        val version: Long,
        val updatedAtMicros: Long,
    ) {
        companion object {
            val MISSING = StateRow(false, "", "", 0L, 0L)
        }
    }

    /**
     * Insert one command row (autocommit) and return the generated id.
     *
     * Uses a prepared statement; the driver caches the parsed statement
     * (see [PgConnectOptions.setCachePreparedStatements] in [build]) so only
     * the first execution per connection pays the parse cost.
     */
    suspend fun insertCommand(
        workflowId: String,
        commandType: String,
        payload: String,
        seq: Long,
        checksum: Long,
    ): Long {
        val rows = pool.preparedQuery(INSERT_COMMAND_SQL)
            .execute(Tuple.of(workflowId, commandType, payload, seq, checksum))
            .coAwait()
        return rows.iterator().next().getLong("id")
    }

    /**
     * Three-statement atomic transaction matching the realistic workflow-engine
     * command path:
     *   1. INSERT into commands (the log)
     *   2. UPSERT workflow_state (advance state, bump version)
     *   3. INSERT into outbox (queue downstream event)
     *
     * Connection is checked out once, BEGIN/COMMIT framed manually so each
     * statement uses the same connection (transactions can't span connections).
     * On exception the transaction is rolled back before the connection
     * returns to the pool.
     */
    suspend fun executeTx(
        workflowId: String,
        commandType: String,
        payload: String,
        seq: Long,
        checksum: Long,
    ): Long {
        val conn = pool.connection.coAwait()
        try {
            val tx = conn.begin().coAwait()
            try {
                val rs1 = conn.preparedQuery(INSERT_COMMAND_SQL)
                    .execute(Tuple.of(workflowId, commandType, payload, seq, checksum))
                    .coAwait()
                val id = rs1.iterator().next().getLong("id")

                conn.preparedQuery(UPSERT_STATE_SQL)
                    .execute(Tuple.of(workflowId, commandType))
                    .coAwait()

                conn.preparedQuery(INSERT_OUTBOX_SQL)
                    .execute(Tuple.of(workflowId, commandType, payload))
                    .coAwait()

                tx.commit().coAwait()
                return id
            } catch (t: Throwable) {
                tx.rollback().coAwait()
                throw t
            }
        } finally {
            conn.close().coAwait()
        }
    }

    /**
     * Single-row read by workflow_id. Returns [StateRow.MISSING] if absent.
     */
    suspend fun getState(workflowId: String): StateRow {
        val rows = pool.preparedQuery(SELECT_STATE_SQL)
            .execute(Tuple.of(workflowId))
            .coAwait()
        val it = rows.iterator()
        if (!it.hasNext()) return StateRow.MISSING
        val r = it.next()
        return StateRow(
            found = true,
            workflowId = workflowId,
            state = r.getString("state"),
            version = r.getLong("version"),
            updatedAtMicros = r.getLong("updated_at_micros"),
        )
    }

    /**
     * Warm the pool up to [min] live connections. Vert.x's reactive pool grows
     * lazily on first use; pgx's pool pre-fills to MinConns. Doing this once
     * at startup keeps the two stacks equivalent at the gun.
     */
    suspend fun warmup(min: Int) {
        if (min <= 0) return
        val held = ArrayList<SqlConnection>(min)
        try {
            repeat(min) {
                held += pool.connection.coAwait()
            }
        } finally {
            held.forEach { it.close() }
        }
    }

    suspend fun close() {
        pool.close().coAwait()
    }

    companion object {
        private const val INSERT_COMMAND_SQL =
            "INSERT INTO commands (workflow_id, command_type, payload, seq, checksum) " +
                "VALUES (\$1, \$2, \$3, \$4, \$5) RETURNING id"

        // UPSERT: insert new workflow_state, or advance state + bump version
        // if the workflow already exists. workflow_state.version starts at 1
        // and increments by 1 per UPDATE.
        private const val UPSERT_STATE_SQL =
            "INSERT INTO workflow_state (workflow_id, state, version, updated_at) " +
                "VALUES (\$1, \$2, 1, now()) " +
                "ON CONFLICT (workflow_id) DO UPDATE SET " +
                "state = EXCLUDED.state, " +
                "version = workflow_state.version + 1, " +
                "updated_at = now()"

        private const val INSERT_OUTBOX_SQL =
            "INSERT INTO outbox (workflow_id, event_type, payload) VALUES (\$1, \$2, \$3)"

        // EXTRACT(EPOCH ...) * 1e6 converted server-side to micros so the
        // driver returns a single Long rather than a TIMESTAMPTZ we'd have to
        // marshal — cheaper allocation per read.
        private const val SELECT_STATE_SQL =
            "SELECT state, version, " +
                "(EXTRACT(EPOCH FROM updated_at) * 1000000)::BIGINT AS updated_at_micros " +
                "FROM workflow_state WHERE workflow_id = \$1"

        fun build(vertx: Vertx, cfg: Config): Db {
            val connectOptions = PgConnectOptions()
                .setHost(cfg.pgHost)
                .setPort(cfg.pgPort)
                .setDatabase(cfg.pgDb)
                .setUser(cfg.pgUser)
                .setPassword(cfg.pgPassword)
                // Mirror pgx's default statement cache.
                .setCachePreparedStatements(true)
                // Reactive-driver pipelining: up to N in-flight queries per
                // connection. This is the architectural advantage we leave on
                // because the production driver would have it on too.
                .setPipeliningLimit(cfg.pipeliningLimit)

            val poolOptions = PoolOptions()
                .setMaxSize(cfg.pgPoolMax)
                // Distribute pool work across the same event loops the gRPC
                // verticles use, so callbacks resume on the calling loop.
                .setEventLoopSize(cfg.eventLoops)

            val pool = PgBuilder.pool()
                .with(poolOptions)
                .connectingTo(connectOptions)
                .using(vertx)
                .build()
            return Db(pool)
        }
    }
}
