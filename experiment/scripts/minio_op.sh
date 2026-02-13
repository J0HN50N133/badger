#!/usr/bin/env bash
set -euo pipefail

ACTION="${1:-start}"
REMOVE_ON_STOP="${2:-}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE_DIR="${MINIO_STATE_DIR:-${SCRIPT_DIR}/../.tmp/minio_state}"
HOST="${MINIO_HOST:-127.0.0.1}"
S3_PORT="${MINIO_S3_PORT:-9000}"
CONSOLE_PORT="${MINIO_CONSOLE_PORT:-9001}"
ROOT_USER="${MINIO_ROOT_USER:-minioadmin}"
ROOT_PASSWORD="${MINIO_ROOT_PASSWORD:-minioadmin}"
BUCKET="${MINIO_BUCKET:-badger-vlog}"
IMAGE="${MINIO_IMAGE:-quay.io/minio/minio:latest}"
CONTAINER_NAME="${MINIO_CONTAINER_NAME:-minio-s3-demo}"

CONTAINER_FILE="${STATE_DIR}/minio.container"

usage() {
  cat <<EOF
Usage:
  $0 start
  $0 stop [--rm]
  $0 --help

Commands:
  start        Start MinIO container if not running.
  stop         Stop MinIO container.
  stop --rm    Stop and remove MinIO container.
EOF
}

need_bin() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required binary: $1" >&2
    exit 1
  fi
}

wait_port() {
  local host="$1"
  local port="$2"
  local retries=80
  while [ "$retries" -gt 0 ]; do
    if (echo >"/dev/tcp/${host}/${port}") >/dev/null 2>&1; then
      return 0
    fi
    retries=$((retries - 1))
    sleep 0.2
  done
  return 1
}

start() {
  need_bin podman
  mkdir -p "$STATE_DIR"

  if podman ps --format '{{.Names}}' | grep -qx "$CONTAINER_NAME"; then
    echo "$CONTAINER_NAME" >"$CONTAINER_FILE"
    echo "minio already running: $CONTAINER_NAME"
  else
    podman rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
    podman run -d --name "$CONTAINER_NAME" \
      -p "${HOST}:${S3_PORT}:9000" \
      -p "${HOST}:${CONSOLE_PORT}:9001" \
      -e "MINIO_ROOT_USER=${ROOT_USER}" \
      -e "MINIO_ROOT_PASSWORD=${ROOT_PASSWORD}" \
      "$IMAGE" server /data --console-address ":9001" >/dev/null
    echo "$CONTAINER_NAME" >"$CONTAINER_FILE"
  fi

  if ! wait_port "$HOST" "$S3_PORT"; then
    echo "minio s3 failed to start" >&2
    echo "podman logs: podman logs $CONTAINER_NAME" >&2
    exit 1
  fi

  echo "minio s3 started"
  echo "endpoint=http://${HOST}:${S3_PORT}"
  echo "bucket=${BUCKET} (will be created by experiment if missing)"
}

stop() {
  need_bin podman
  local remove="false"
  if [ "$REMOVE_ON_STOP" = "--rm" ]; then
    remove="true"
  elif [ -n "$REMOVE_ON_STOP" ]; then
    echo "unknown stop option: $REMOVE_ON_STOP" >&2
    echo "usage: $0 stop [--rm]" >&2
    exit 2
  fi

  local name="$CONTAINER_NAME"
  if [ -f "$CONTAINER_FILE" ]; then
    name="$(cat "$CONTAINER_FILE" || true)"
    rm -f "$CONTAINER_FILE"
  fi

  if [ -n "$name" ]; then
    podman stop "$name" >/dev/null 2>&1 || true
    if [ "$remove" = "true" ]; then
      podman rm "$name" >/dev/null 2>&1 || true
      echo "minio stopped and removed: $name"
      return 0
    fi
    echo "minio stopped: $name"
    return 0
  fi

  echo "minio not running"
}

case "$ACTION" in
  -h|--help|help)
    usage
    ;;
  start)
    start
    ;;
  stop)
    stop
    ;;
  *)
    echo "invalid action: $ACTION" >&2
    usage >&2
    exit 2
    ;;
esac
