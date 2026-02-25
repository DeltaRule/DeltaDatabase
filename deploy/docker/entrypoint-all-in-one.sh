#!/bin/sh
# Start both the Main Worker and one Processing Worker inside a single container.
# The Main Worker must be ready before the Processing Worker subscribes.
set -e

MASTER_KEY="${MASTER_KEY:-}"
ADMIN_KEY="${ADMIN_KEY:-}"
SHARED_FS="${SHARED_FS:-/shared/db}"

echo "[entrypoint] Starting Main Worker…"
main-worker \
  -grpc-addr=0.0.0.0:50051 \
  -rest-addr=0.0.0.0:8080 \
  -shared-fs="${SHARED_FS}" \
  ${MASTER_KEY:+-master-key="${MASTER_KEY}"} \
  ${ADMIN_KEY:+-admin-key="${ADMIN_KEY}"} &
MAIN_PID=$!

# Give the Main Worker a moment to bind its ports before the worker connects.
sleep 2

echo "[entrypoint] Starting Processing Worker…"
proc-worker \
  -main-addr=127.0.0.1:50051 \
  -worker-id=proc-1 \
  -grpc-addr=0.0.0.0:50052 \
  -shared-fs="${SHARED_FS}" &
PROC_PID=$!

echo "[entrypoint] Both processes running (main PID=${MAIN_PID}, proc PID=${PROC_PID})"

# Forward SIGTERM/SIGINT to both children.
trap 'kill ${MAIN_PID} ${PROC_PID} 2>/dev/null' TERM INT

wait ${MAIN_PID} ${PROC_PID}
