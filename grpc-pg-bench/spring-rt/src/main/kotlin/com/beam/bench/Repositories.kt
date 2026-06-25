package com.beam.bench

import org.springframework.data.r2dbc.repository.Modifying
import org.springframework.data.r2dbc.repository.Query
import org.springframework.data.repository.kotlin.CoroutineCrudRepository
import org.springframework.data.repository.query.Param

/**
 * Spring Data R2DBC repositories, Kotlin-coroutine flavour
 * ([CoroutineCrudRepository] — every method is a `suspend fun`, fully
 * non-blocking end to end). The reactive analogue of the spring-data-jdbc
 * repositories: `save()` for inserts, a native `@Modifying @Query` for the
 * conflict-aware UPSERT (Spring Data can only INSERT *or* UPDATE via `save()`,
 * never UPSERT).
 */

interface CommandRepository : CoroutineCrudRepository<Command, Long>

interface OutboxRepository : CoroutineCrudRepository<OutboxEvent, Long>

interface WorkflowStateRepository : CoroutineCrudRepository<WorkflowState, String> {

    /** Conflict-aware insert; SQL identical to the other stacks' UPSERT. */
    @Modifying
    @Query(
        """
        INSERT INTO workflow_state (workflow_id, state, version, updated_at)
        VALUES (:wid, :state, 1, now())
        ON CONFLICT (workflow_id) DO UPDATE SET
            state = EXCLUDED.state,
            version = workflow_state.version + 1,
            updated_at = now()
        """,
    )
    suspend fun upsert(@Param("wid") workflowId: String, @Param("state") state: String): Long
}
