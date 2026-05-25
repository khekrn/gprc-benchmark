#!/usr/bin/env bash
# Create the bench role + database and apply the schema.
# Assumes you can connect to Postgres as a superuser (default: current user
# via local socket, or set PGSUPERUSER / PGSUPERPASS).
#
# Usage: ./scripts/setup_db.sh
set -euo pipefail
cd "$(dirname "$0")"
source ./config.sh

SUPER="${PGSUPERUSER:-postgres}"
ADMIN_CONN="${ADMIN_CONN:-postgres://${SUPER}@${PG_HOST}:${PG_PORT}/postgres}"

echo ">> Creating role '${PG_USER}' and database '${PG_DB}' (idempotent)"
psql "${ADMIN_CONN}" -v ON_ERROR_STOP=1 <<SQL || true
DO \$\$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '${PG_USER}') THEN
    CREATE ROLE ${PG_USER} LOGIN PASSWORD '${PG_PASSWORD}';
  END IF;
END
\$\$;
SQL

# CREATE DATABASE cannot run inside the DO block / a transaction.
if ! psql "${ADMIN_CONN}" -tAc "SELECT 1 FROM pg_database WHERE datname='${PG_DB}'" | grep -q 1; then
  psql "${ADMIN_CONN}" -v ON_ERROR_STOP=1 -c "CREATE DATABASE ${PG_DB} OWNER ${PG_USER};"
fi

echo ">> Applying schema"
PGPASSWORD="${PG_PASSWORD}" psql "${DATABASE_URL}" -v ON_ERROR_STOP=1 -f "${ROOT_DIR}/sql/schema.sql"

echo ">> Done. Table 'commands' is ready in '${PG_DB}'."
