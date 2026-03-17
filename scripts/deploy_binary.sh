#!/usr/bin/env bash
set -euo pipefail

: "${DEPLOY_HOST:?DEPLOY_HOST is required}"
: "${DEPLOY_USER:?DEPLOY_USER is required}"
: "${DEPLOY_DIR:?DEPLOY_DIR is required}"
: "${SERVICE_NAME:?SERVICE_NAME is required}"
: "${LOCAL_BINARY:?LOCAL_BINARY is required}"

REMOTE_BIN_DIR="${DEPLOY_DIR}/bin"
REMOTE_CONF_DIR="${DEPLOY_DIR}/conf"
REMOTE_RELEASE_DIR="${DEPLOY_DIR}/releases"
REMOTE_BACKUP_DIR="${REMOTE_RELEASE_DIR}/backup"
REMOTE_LOG_DIR="${REMOTE_LOG_DIR:-/var/log/${SERVICE_NAME}}"
REMOTE_UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"
REMOTE_BINARY_PATH="${REMOTE_BIN_DIR}/${SERVICE_NAME}"

LOCAL_ENV_FILE="${LOCAL_ENV_FILE:-}"
LOCAL_SYSTEMD_FILE="${LOCAL_SYSTEMD_FILE:-}"
HEALTHCHECK_URL="${HEALTHCHECK_URL:-http://127.0.0.1:8080/healthz}"
HEALTHCHECK_RETRIES="${HEALTHCHECK_RETRIES:-12}"
HEALTHCHECK_INTERVAL_SEC="${HEALTHCHECK_INTERVAL_SEC:-5}"

SSH_TARGET="${DEPLOY_USER}@${DEPLOY_HOST}"

echo "[1/6] preparing remote directories on ${DEPLOY_HOST}"
ssh "${SSH_TARGET}" "mkdir -p '${REMOTE_BIN_DIR}' '${REMOTE_CONF_DIR}' '${REMOTE_RELEASE_DIR}' '${REMOTE_BACKUP_DIR}' && sudo mkdir -p '${REMOTE_LOG_DIR}'"

echo "[2/6] backing up previous binary if present"
ssh "${SSH_TARGET}" "if [ -f '${REMOTE_BINARY_PATH}' ]; then cp '${REMOTE_BINARY_PATH}' '${REMOTE_BACKUP_DIR}/${SERVICE_NAME}-$(date +%Y%m%d%H%M%S)'; fi"

echo "[3/6] uploading binary"
scp "${LOCAL_BINARY}" "${SSH_TARGET}:${REMOTE_BINARY_PATH}"
ssh "${SSH_TARGET}" "chmod +x '${REMOTE_BINARY_PATH}'"

if [[ -n "${LOCAL_ENV_FILE}" ]]; then
  echo "[4/6] uploading runtime config"
  scp "${LOCAL_ENV_FILE}" "${SSH_TARGET}:${REMOTE_CONF_DIR}/config.env"
fi

if [[ -n "${LOCAL_SYSTEMD_FILE}" ]]; then
  echo "[4/6] installing systemd unit"
  scp "${LOCAL_SYSTEMD_FILE}" "${SSH_TARGET}:/tmp/${SERVICE_NAME}.service"
  ssh "${SSH_TARGET}" "sudo mv '/tmp/${SERVICE_NAME}.service' '${REMOTE_UNIT_PATH}'"
fi

echo "[5/6] restarting service"
ssh "${SSH_TARGET}" "sudo systemctl daemon-reload && sudo systemctl enable '${SERVICE_NAME}.service' && sudo systemctl restart '${SERVICE_NAME}.service'"

echo "[6/6] verifying health endpoint ${HEALTHCHECK_URL}"
for ((i=1; i<=HEALTHCHECK_RETRIES; i++)); do
  if ssh "${SSH_TARGET}" "curl -fsS '${HEALTHCHECK_URL}'"; then
    echo
    echo "deployment finished successfully"
    exit 0
  fi
  echo "health check attempt ${i}/${HEALTHCHECK_RETRIES} failed, retrying in ${HEALTHCHECK_INTERVAL_SEC}s"
  sleep "${HEALTHCHECK_INTERVAL_SEC}"
done

echo "deployment failed: health check did not pass"
ssh "${SSH_TARGET}" "sudo systemctl status '${SERVICE_NAME}.service' --no-pager || true"
exit 1
