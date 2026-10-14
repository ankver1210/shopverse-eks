#!/bin/bash
# ---------------------------------------------------------------------------
# Runs ONCE, the first time PostgreSQL starts with an empty data volume.
# Implements the "database per service" pattern: every microservice gets its own
# database and its own login role, so no service can read another one's tables.
#
# Passwords come from environment variables (fed from a Kubernetes Secret that
# External Secrets syncs from AWS Secrets Manager).
# ---------------------------------------------------------------------------
set -euo pipefail

create_service_db() {
  local db="$1" role="$2" password="$3"
  echo "creating database '${db}' owned by role '${role}'"
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
       -v db="$db" -v role="$role" -v pw="$password" <<'EOSQL'
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'role', :'pw') \gexec
SELECT format('CREATE DATABASE %I OWNER %I', :'db', :'role') \gexec
SELECT format('REVOKE ALL ON DATABASE %I FROM PUBLIC', :'db') \gexec
EOSQL
}

create_service_db users     user_service      "${USER_SERVICE_DB_PASSWORD}"
create_service_db products  product_service   "${PRODUCT_SERVICE_DB_PASSWORD}"
create_service_db orders    order_service     "${ORDER_SERVICE_DB_PASSWORD}"
create_service_db inventory inventory_service "${INVENTORY_SERVICE_DB_PASSWORD}"
create_service_db payments  payment_service   "${PAYMENT_SERVICE_DB_PASSWORD}"
echo "all service databases created"
