#!/bin/bash
set -e

DATA_DIR="/tmp/stackfly-dev"

echo "==> Building StackFly..."
cd /app
go build -o /tmp/stackfly ./cmd/stackfly

# Make data dir writable from host for git push testing
mkdir -p "$DATA_DIR"
chmod -R 777 "$DATA_DIR"

echo "==> Starting StackFly (data: $DATA_DIR, bind: 0.0.0.0)"
exec /tmp/stackfly --data-dir "$DATA_DIR" --bind 0.0.0.0
