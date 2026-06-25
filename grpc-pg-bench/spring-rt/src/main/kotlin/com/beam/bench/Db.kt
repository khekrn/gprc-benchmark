package com.beam.bench

import org.springframework.stereotype.Component
import org.springframework.transaction.annotation.Transactional
import java.time.Instant

/**
 * Fully non-blocking data-access over **Spring Data R2DBC** repositories
 * ([CoroutineCrudRepository], suspend functions). The reactive twin of
 * spring-data-jdbc: same operations, same SQL, same R2DBC pool — but through the
 * repository abstraction (entities + `save()` + a `@Modifying` upsert) instead
 * of the lower-level `DatabaseClient`. Every method suspends the calling
 * coroutine while Postgres works; no thread is blocked.
 */
@Component
class Db(
    private val commands: CommandRepository,
    private val states: WorkflowStateRepository,
    private val outbox: OutboxRepository,
) {

    /** Result of [getState]. */
    data class StateRow(
        val workflowId: String,
        val state: String,
        val version: Long,
        val updatedAtMicros: Long,
    )

    /**
     * Single INSERT via `save()`; resolves to the generated id. Profiling showed
     * `save()` carries per-call SQL-rendering + reflection + a transactional
     * wrapper, but a hand-rolled `@Query` autocommit insert measured *equal*
     * throughput — the reactive path is I/O-wait / cross-event-loop-handoff
     * bound, not CPU/alloc bound, so `save()` (the idiomatic Spring Data R2DBC
     * call, consistent with spring-data-jdbc) is kept.
     */
    suspend fun insertCommand(
        workflowId: String,
        commandType: String,
        payload: String,
        seq: Long,
        checksum: Long,
    ): Long =
        commands.save(
            Command(
                workflowId = workflowId,
                commandType = commandType,
                payload = payload,
                seq = seq,
                checksum = checksum,
            ),
        ).id!!

    /**
     * Three-statement atomic transaction (INSERT command + UPSERT state +
     * INSERT outbox), each suspending in turn inside one reactive transaction.
     * `@Transactional` on a suspend function uses Boot's reactive
     * `ReactiveTransactionManager`, so the BEGIN/COMMIT wrap all three repository
     * calls (which enlist on the same R2DBC connection) — no thread blocked.
     */
    @Transactional
    suspend fun executeTx(
        workflowId: String,
        commandType: String,
        payload: String,
        seq: Long,
        checksum: Long,
    ): Long {
        val id = commands.save(
            Command(
                workflowId = workflowId,
                commandType = commandType,
                payload = payload,
                seq = seq,
                checksum = checksum,
            ),
        ).id!!
        states.upsert(workflowId, commandType)
        outbox.save(OutboxEvent(workflowId = workflowId, eventType = commandType, payload = payload))
        return id
    }

    /** Single-row read by primary key; null if absent. */
    suspend fun getState(workflowId: String): StateRow? =
        states.findById(workflowId)?.let { s ->
            StateRow(
                workflowId = s.workflowId,
                state = s.state,
                version = s.version,
                updatedAtMicros = toMicros(s.updatedAt),
            )
        }

    private companion object {
        /** Unix micros from an Instant — matches the BIGINT the SQL stacks return. */
        fun toMicros(ts: Instant): Long = ts.epochSecond * 1_000_000L + ts.nano / 1_000L
    }
}
