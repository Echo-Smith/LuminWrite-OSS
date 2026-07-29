#!/bin/bash
# init-weknora-db.sh — Create the WeKnora database if it doesn't exist.
# Mounted into PostgreSQL's docker-entrypoint-initdb.d/ for automatic execution on first init.
set -e

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" <<-EOSQL
  SELECT 'CREATE DATABASE weknora'
  WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'weknora')\gexec
  GRANT ALL PRIVILEGES ON DATABASE weknora TO $POSTGRES_USER;
EOSQL

echo "[init-weknora-db] database 'weknora' ensured."
