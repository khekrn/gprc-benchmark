package com.beam.bench

import org.springframework.data.annotation.Id
import org.springframework.data.relational.core.mapping.Column
import org.springframework.data.relational.core.mapping.Table

/**
 * Spring Data R2DBC entity for the `outbox` table. Only the columns the other
 * stacks write are mapped; `created_at` (`DEFAULT now()`) and `dispatched`
 * (`DEFAULT false`) are left to the DB defaults, so the generated INSERT matches
 * the hand-written one in the other stacks.
 */
@Table("outbox")
data class OutboxEvent(
    @Id val id: Long? = null,
    @Column("workflow_id") val workflowId: String,
    @Column("event_type") val eventType: String,
    val payload: String,
)
