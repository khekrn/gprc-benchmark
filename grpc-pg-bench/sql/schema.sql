-- Schema for the gRPC + Postgres benchmark.
-- Two paths exercised:
--   1. Execute (autocommit)         — single INSERT into commands
--   2. ExecuteTx (multi-stmt TX)    — INSERT command + UPSERT state + INSERT outbox
--   3. GetState (read)              — single SELECT from workflow_state
-- All CREATEs are IF NOT EXISTS so this is safe to re-apply.

CREATE TABLE IF NOT EXISTS commands (
    id              BIGSERIAL  PRIMARY KEY,
    workflow_id     TEXT        NOT NULL,
    command_type    TEXT        NOT NULL,
    payload         TEXT        NOT NULL,
    seq             BIGINT      NOT NULL,
    checksum        BIGINT      NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_commands_workflow ON commands (workflow_id);

-- Per-workflow current state. UPSERTed inside ExecuteTx so the same
-- workflow_id may be inserted once and then updated thousands of times,
-- exercising both the INSERT and the UPDATE paths.
CREATE TABLE IF NOT EXISTS workflow_state (
    workflow_id   TEXT        PRIMARY KEY,
    state         TEXT        NOT NULL,
    version       BIGINT      NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Outbox for downstream events: one row per command in the same transaction
-- as the command itself. The bench does not run the dispatcher; the
-- (dispatched, created_at) index simulates the write-amplification a real
-- dispatcher index would impose on every INSERT.
CREATE TABLE IF NOT EXISTS outbox (
    id            BIGSERIAL   PRIMARY KEY,
    workflow_id   TEXT        NOT NULL,
    event_type    TEXT        NOT NULL,
    payload       TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    dispatched    BOOLEAN     NOT NULL DEFAULT FALSE
);
CREATE INDEX IF NOT EXISTS idx_outbox_dispatch ON outbox (dispatched, created_at);

-- Wipe between runs without dropping tables. Sequences reset to 1.
-- Usage: psql ... -c 'TRUNCATE commands, workflow_state, outbox RESTART IDENTITY;'
