#!/bin/sh
set -e

if [ -z "$RMM_DATABASE_URL" ]; then
  echo "ERROR: RMM_DATABASE_URL is required" >&2
  exit 1
fi

echo "Running database migrations..."
migrate -path /app/migrations -database "$RMM_DATABASE_URL" up

echo "Starting rmmserver..."
exec /app/rmmserver "$@"
