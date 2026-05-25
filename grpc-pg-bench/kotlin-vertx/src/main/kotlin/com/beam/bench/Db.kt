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

    /**
     * Insert one command row and return the generated id.
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
        val rows = pool.preparedQuery(INSERT_SQL)
            .execute(Tuple.of(workflowId, commandType, payload, seq, checksum))
            .coAwait()
        return rows.iterator().next().getLong("id")
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
        private const val INSERT_SQL =
            "INSERT INTO commands (workflow_id, command_type, payload, seq, checksum) " +
                "VALUES (\$1, \$2, \$3, \$4, \$5) RETURNING id"

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
