-- Schema for the gRPC + Postgres benchmark.
-- Kept deliberately minimal: one table, one index, one insert path.
-- Run once before benchmarking each stack (or use scripts/setup_db.sh).

CREATE TABLE IF NOT EXISTS commands (
    id              BIGSERIAL  PRIMARY KEY,
    workflow_id     TEXT        NOT NULL,
    command_type    TEXT        NOT NULL,
    payload         TEXT        NOT NULL,
    seq             BIGINT      NOT NULL,
    checksum        BIGINT      NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A realistic-but-cheap index so the INSERT pays a small write-amplification
-- cost, the same on both stacks.
CREATE INDEX IF NOT EXISTS idx_commands_workflow ON commands (workflow_id);

-- Helper to wipe between runs without dropping the table.
-- TRUNCATE is faster than DELETE and resets the sequence.
-- Usage: psql ... -c 'TRUNCATE commands RESTART IDENTITY;'
