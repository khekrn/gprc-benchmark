CREATE TABLE IF NOT EXISTS users (
    id                        TEXT PRIMARY KEY,
    email                     TEXT NOT NULL UNIQUE,
    name                      TEXT NOT NULL,
    created_at_epoch_millis   BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
