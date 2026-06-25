package com.beam.bench;

import org.springframework.data.jdbc.repository.query.Modifying;
import org.springframework.data.jdbc.repository.query.Query;
import org.springframework.data.repository.CrudRepository;
import org.springframework.data.repository.query.Param;

/**
 * Spring Data JDBC repository for {@link WorkflowState}. The read path uses the
 * inherited {@code findById}; the write path uses {@link #upsert}, a native
 * conflict-aware INSERT (Spring Data JDBC's {@code save()} can only INSERT *or*
 * UPDATE, never UPSERT). SQL is byte-identical to spring-vt's UPSERT_STATE_SQL,
 * only the placeholders are named ({@code :wid}) instead of positional.
 */
interface WorkflowStateRepository extends CrudRepository<WorkflowState, String> {

    @Modifying
    @Query("""
            INSERT INTO workflow_state (workflow_id, state, version, updated_at)
            VALUES (:wid, :state, 1, now())
            ON CONFLICT (workflow_id) DO UPDATE SET
                state = EXCLUDED.state,
                version = workflow_state.version + 1,
                updated_at = now()
            """)
    void upsert(@Param("wid") String workflowId, @Param("state") String state);
}
