package com.beam.bench;

import org.springframework.data.annotation.Id;
import org.springframework.data.relational.core.mapping.Column;
import org.springframework.data.relational.core.mapping.Table;

/**
 * Spring Data JDBC entity for the {@code outbox} table. Only the columns the
 * other stacks write are mapped; {@code created_at} ({@code DEFAULT now()}) and
 * {@code dispatched} ({@code DEFAULT false}) are left to the DB defaults, so the
 * generated INSERT matches the hand-written one in spring-vt.
 */
@Table("outbox")
record OutboxEvent(
        @Id Long id,
        @Column("workflow_id") String workflowId,
        @Column("event_type") String eventType,
        String payload) {
}
