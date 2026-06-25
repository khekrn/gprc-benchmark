package com.beam.bench;

import org.springframework.data.annotation.Id;
import org.springframework.data.relational.core.mapping.Column;
import org.springframework.data.relational.core.mapping.Table;

/**
 * Spring Data JDBC entity for the {@code commands} table. Immutable record:
 * Spring Data JDBC constructs new instances via the canonical constructor and,
 * on insert, returns a fresh copy with the DB-generated {@code id} populated.
 *
 * {@code id == null} marks the row as new, so {@code save()} issues an INSERT
 * (and fetches the generated key). {@code received_at} is intentionally
 * unmapped — the column's {@code DEFAULT now()} fills it, matching the other
 * stacks (which also let Postgres stamp it).
 */
@Table("commands")
record Command(
        @Id Long id,
        @Column("workflow_id") String workflowId,
        @Column("command_type") String commandType,
        String payload,
        long seq,
        long checksum) {
}
