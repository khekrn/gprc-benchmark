-- Schema applied by the app on startup (see src/migrate.rs). Idempotent.

CREATE TABLE IF NOT EXISTS users (
    id          BIGSERIAL PRIMARY KEY,
    email       VARCHAR(320) NOT NULL UNIQUE,
    full_name   VARCHAR(200) NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS users_email_idx ON users (email);

-- Used by the LISTEN/NOTIFY hook (Repository::listen_for_new_users).
CREATE OR REPLACE FUNCTION notify_user_created() RETURNS TRIGGER AS $$
BEGIN
    PERFORM pg_notify('users_created', NEW.id::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS users_created_notify ON users;
CREATE TRIGGER users_created_notify
AFTER INSERT ON users
FOR EACH ROW EXECUTE FUNCTION notify_user_created();
