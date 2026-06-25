package com.beam.bench

import org.springframework.data.annotation.Id
import org.springframework.data.relational.core.mapping.Column
import org.springframework.data.relational.core.mapping.Table

/**
 * Spring Data R2DBC entity for the `commands` table. `id == null` marks the row
 * as new, so `save()` issues an INSERT (r2dbc-postgresql fetches the generated
 * key via `RETURNING id`). `received_at` is intentionally unmapped — the column
 * `DEFAULT now()` fills it, matching the other stacks.
 */
@Table("commands")
data class Command(
    @Id val id: Long? = null,
    @Column("workflow_id") val workflowId: String,
    @Column("command_type") val commandType: String,
    val payload: String,
    val seq: Long,
    val checksum: Long,
)
