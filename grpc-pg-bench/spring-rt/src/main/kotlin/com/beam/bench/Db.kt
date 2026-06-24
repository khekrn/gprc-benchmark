package com.beam.bench

import kotlinx.coroutines.reactive.awaitFirstOrNull
import kotlinx.coroutines.reactive.awaitSingle
import org.springframework.r2dbc.core.DatabaseClient
import org.springframework.stereotype.Component
import org.springframework.transaction.reactive.TransactionalOperator
import org.springframework.transaction.reactive.executeAndAwait

/**
 * Fully non-blocking data-access over R2DBC via Spring's [DatabaseClient].
 * Every method is a `suspend fun`; the underlying reactive `Mono`s are awaited
 * with [awaitSingle]/[awaitSingleOrNull] so the calling coroutine suspends (no
 * thread blocked) while Postgres works. Same SQL as kotlin-vertx ($1 markers).
 */
@Component
class Db(
    private val client: DatabaseClient,
    private val tx: TransactionalOperator,
) {

    /** Result of [getState]. */
    data class StateRow(
        val workflowId: String,
        val state: String,
        val version: Long,
        val updatedAtMicros: Long,
    )

    /** Single autocommit INSERT; resolves to the generated id. */
    suspend fun insertCommand(
        workflowId: String,
        commandType: String,
        payload: String,
        seq: Long,
        checksum: Long,
    ): Long =
        client.sql(INSERT_COMMAND_SQL)
            .bind(0, workflowId)
            .bind(1, commandType)
            .bind(2, payload)
            .bind(3, seq)
            .bind(4, checksum)
            .map { row -> row.get(0, java.lang.Long::class.java)!!.toLong() }
            .one()
            .awaitSingle()

    /**
     * Three-statement atomic transaction (INSERT command + UPSERT state +
     * INSERT outbox), chained sequentially inside one reactive transaction —
     * the same per-statement model every stack uses. [executeAndAwait] opens
     * the transaction, commits on success and rolls back on any thrown error.
     */
    suspend fun executeTx(
        workflowId: String,
        commandType: String,
        payload: String,
        seq: Long,
        checksum: Long,
    ): Long = tx.executeAndAwait {
        val id = client.sql(INSERT_COMMAND_SQL)
            .bind(0, workflowId)
            .bind(1, commandType)
            .bind(2, payload)
            .bind(3, seq)
            .bind(4, checksum)
            .map { row -> row.get(0, java.lang.Long::class.java)!!.toLong() }
            .one()
            .awaitSingle()

        client.sql(UPSERT_STATE_SQL)
            .bind(0, workflowId)
            .bind(1, commandType)
            .fetch().rowsUpdated().awaitSingle()

        client.sql(INSERT_OUTBOX_SQL)
            .bind(0, workflowId)
            .bind(1, commandType)
            .bind(2, payload)
            .fetch().rowsUpdated().awaitSingle()

        id
    }!!

    /** Single-row read by workflow_id; null if absent. */
    suspend fun getState(workflowId: String): StateRow? =
        client.sql(SELECT_STATE_SQL)
            .bind(0, workflowId)
            .map { row ->
                StateRow(
                    workflowId = workflowId,
                    state = row.get("state", String::class.java) ?: "",
                    version = row.get("version", java.lang.Long::class.java)!!.toLong(),
                    updatedAtMicros = row.get("updated_at_micros", java.lang.Long::class.java)!!.toLong(),
                )
            }
            .one()
            .awaitFirstOrNull()

    companion object {
        private const val INSERT_COMMAND_SQL =
            "INSERT INTO commands (workflow_id, command_type, payload, seq, checksum) " +
                "VALUES (\$1, \$2, \$3, \$4, \$5) RETURNING id"

        private const val UPSERT_STATE_SQL =
            "INSERT INTO workflow_state (workflow_id, state, version, updated_at) " +
                "VALUES (\$1, \$2, 1, now()) " +
                "ON CONFLICT (workflow_id) DO UPDATE SET " +
                "state = EXCLUDED.state, " +
                "version = workflow_state.version + 1, " +
                "updated_at = now()"

        private const val INSERT_OUTBOX_SQL =
            "INSERT INTO outbox (workflow_id, event_type, payload) VALUES (\$1, \$2, \$3)"

        private const val SELECT_STATE_SQL =
            "SELECT state, version, " +
                "(EXTRACT(EPOCH FROM updated_at) * 1000000)::BIGINT AS updated_at_micros " +
                "FROM workflow_state WHERE workflow_id = \$1"
    }
}
