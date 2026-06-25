package com.beam.bench

import org.springframework.data.annotation.Id
import org.springframework.data.relational.core.mapping.Column
import org.springframework.data.relational.core.mapping.Table
import java.time.Instant

/**
 * Spring Data R2DBC entity for the `workflow_state` table. The primary key is
 * the natural key `workflow_id` (a String), so it is never null for an existing
 * row — which is why the UPSERT cannot go through `save()` (Spring Data R2DBC
 * would treat a non-null @Id as an UPDATE). The conflict-aware INSERT lives in
 * [WorkflowStateRepository.upsert]; this entity is only used on the read path.
 */
@Table("workflow_state")
data class WorkflowState(
    @Id @Column("workflow_id") val workflowId: String,
    val state: String,
    val version: Long,
    @Column("updated_at") val updatedAt: Instant,
)
