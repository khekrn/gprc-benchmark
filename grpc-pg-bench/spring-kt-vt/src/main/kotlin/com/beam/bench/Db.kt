package com.beam.bench

import org.jetbrains.exposed.v1.core.Function
import org.jetbrains.exposed.v1.core.LongColumnType
import org.jetbrains.exposed.v1.core.QueryBuilder
import org.jetbrains.exposed.v1.core.Table
import org.jetbrains.exposed.v1.core.eq
import org.jetbrains.exposed.v1.core.plus
import org.jetbrains.exposed.v1.core.statements.insertValue
import org.jetbrains.exposed.v1.jdbc.insert
import org.jetbrains.exposed.v1.jdbc.insertReturning
import org.jetbrains.exposed.v1.jdbc.select
import org.jetbrains.exposed.v1.jdbc.upsert
import org.springframework.stereotype.Component
import org.springframework.transaction.annotation.Transactional

/**
 * Exposed table mappings for the existing benchmark schema (sql/schema.sql).
 * We never CREATE here — setup_db.sh owns the DDL; these objects just map the
 * columns we read/write. Columns with DB-side defaults (received_at,
 * updated_at, created_at, dispatched) are intentionally omitted so Postgres
 * fills them.
 */
internal object Commands : Table("commands") {
    val id = long("id").autoIncrement()
    val workflowId = text("workflow_id")
    val commandType = text("command_type")
    val payload = text("payload")
    val seq = long("seq")
    val checksum = long("checksum")
    override val primaryKey = PrimaryKey(id)
}

internal object WorkflowState : Table("workflow_state") {
    val workflowId = text("workflow_id")
    val state = text("state")
    val version = long("version")
    override val primaryKey = PrimaryKey(workflowId)
}

internal object Outbox : Table("outbox") {
    val id = long("id").autoIncrement()
    val workflowId = text("workflow_id")
    val eventType = text("event_type")
    val payload = text("payload")
    override val primaryKey = PrimaryKey(id)
}

/**
 * Server-side conversion of workflow_state.updated_at to unix micros, mirroring
 * the other stacks' SELECT exactly: `(EXTRACT(EPOCH FROM updated_at)*1e6)::BIGINT`.
 * Doing it in SQL returns a single Long instead of a TIMESTAMPTZ to marshal.
 */
internal object UpdatedAtMicros : Function<Long>(LongColumnType()) {
    override fun toQueryBuilder(queryBuilder: QueryBuilder) {
        queryBuilder.append("(EXTRACT(EPOCH FROM updated_at) * 1000000)::BIGINT")
    }
}

/**
 * Data access via the **Exposed DSL** (type-safe query builder) on the JDBC
 * backend, executed on the calling (virtual) thread. This is the deliberate
 * difference from the Java `spring-vt` stack: instead of hand-written SQL +
 * raw JDBC, the queries are expressed through Exposed's DSL, so the benchmark
 * measures the framework's per-call cost on virtual threads.
 *
 * Integration follows the official JetBrains `samples/exposed-spring` (Boot 4):
 * the class is `@Transactional`, so Spring's `SpringTransactionManager` (from
 * the `exposed-spring-boot4-starter` autoconfig) opens the transaction and
 * binds a pooled connection — the Exposed DSL calls then run directly, with no
 * manual `Database.connect()` or `transaction { }` wrapper. Each public method
 * is its own transaction boundary; `executeTx`'s three statements share one.
 * HikariCP is still the pool underneath (env-sized, min=4/max=16 for
 * cross-stack fairness); on a virtual thread the blocking JDBC call parks the
 * carrier thread on I/O, exactly like the raw-JDBC `spring-vt` path.
 *
 * NOTE ON FAIRNESS: unlike the raw-SQL stacks, Exposed *generates* its own SQL
 * from the DSL. The operations are semantically identical (same tables, same
 * plans), but the statement text is the framework's, not the shared literal —
 * so this stack is an "Exposed framework cost" data point, not a byte-identical
 * SQL comparison.
 */
@Component
@Transactional
class Db {

    /** Result of [getState]. `found == false` means no row. */
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

    /** Single autocommit INSERT; returns the generated id. */
    fun insertCommand(
        workflowId: String,
        commandType: String,
        payload: String,
        seq: Long,
        checksum: Long,
    ): Long {
        // RETURNING id only (not the whole row) to match the other stacks and
        // avoid shipping the payload back on the write path.
        return Commands.insertReturning(listOf(Commands.id)) {
            it[Commands.workflowId] = workflowId
            it[Commands.commandType] = commandType
            it[Commands.payload] = payload
            it[Commands.seq] = seq
            it[Commands.checksum] = checksum
        }.single()[Commands.id]
    }

    /**
     * Three-statement atomic transaction (INSERT command + UPSERT state +
     * INSERT outbox). All three run inside one [transaction] block, so Exposed
     * frames a single BEGIN/COMMIT and rolls back on any thrown exception. The
     * statements are issued sequentially (the per-statement model every stack
     * shares); Exposed does not pipeline them.
     */
    fun executeTx(
        workflowId: String,
        commandType: String,
        payload: String,
        seq: Long,
        checksum: Long,
    ): Long {
        val id = Commands.insertReturning(listOf(Commands.id)) {
            it[Commands.workflowId] = workflowId
            it[Commands.commandType] = commandType
            it[Commands.payload] = payload
            it[Commands.seq] = seq
            it[Commands.checksum] = checksum
        }.single()[Commands.id]

        // INSERT new workflow_state, or advance state + bump version if the
        // workflow already exists. version starts at 1 and +1 per UPDATE.
        WorkflowState.upsert(
            WorkflowState.workflowId,
            onUpdate = {
                // EXCLUDED.state — the value that would have been inserted.
                it[WorkflowState.state] = insertValue(WorkflowState.state)
                // version = workflow_state.version + 1, via the top-level `plus`
                // expression builder (the scoped receiver was removed in 1.0).
                it[WorkflowState.version] = WorkflowState.version + 1L
            },
        ) {
            it[WorkflowState.workflowId] = workflowId
            it[WorkflowState.state] = commandType
            it[WorkflowState.version] = 1L
        }

        Outbox.insert {
            it[Outbox.workflowId] = workflowId
            it[Outbox.eventType] = commandType
            it[Outbox.payload] = payload
        }
        return id
    }

    /** Single-row read by workflow_id; [StateRow.MISSING] if absent. */
    fun getState(workflowId: String): StateRow {
        val row = WorkflowState
            .select(WorkflowState.state, WorkflowState.version, UpdatedAtMicros)
            .where { WorkflowState.workflowId eq workflowId }
            .singleOrNull()
            ?: return StateRow.MISSING
        return StateRow(
            found = true,
            workflowId = workflowId,
            state = row[WorkflowState.state],
            version = row[WorkflowState.version],
            updatedAtMicros = row[UpdatedAtMicros],
        )
    }
}
