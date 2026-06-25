package com.beam.bench;

import org.springframework.data.annotation.Id;
import org.springframework.data.relational.core.mapping.Column;
import org.springframework.data.relational.core.mapping.Table;

import java.time.Instant;

/**
 * Spring Data JDBC entity for the {@code workflow_state} table. The primary key
 * is the natural key {@code workflow_id} (a String), so it is never null for an
 * existing row — which is exactly why the UPSERT cannot go through
 * {@code save()} (Spring Data JDBC would treat a non-null @Id as an UPDATE).
 * The conflict-aware INSERT lives in {@link WorkflowStateRepository#upsert} as a
 * native {@code @Modifying @Query}; this entity is only used on the read path
 * ({@code findById}).
 */
@Table("workflow_state")
record WorkflowState(
        @Id @Column("workflow_id") String workflowId,
        String state,
        long version,
        @Column("updated_at") Instant updatedAt) {
}
