#!/usr/bin/env bash
set -euo pipefail

: "${DEPLOY_DIR:?DEPLOY_DIR is required}"
: "${SERVICE_NAME:?SERVICE_NAME is required}"
: "${LOCAL_BINARY:?LOCAL_BINARY is required}"

DEPLOY_BIN_DIR="${DEPLOY_DIR}/bin"
DEPLOY_CONF_DIR="${DEPLOY_DIR}/conf"
DEPLOY_RELEASE_DIR="${DEPLOY_DIR}/releases"
DEPLOY_BACKUP_DIR="${DEPLOY_RELEASE_DIR}/backup"
DEPLOY_LOG_DIR="${DEPLOY_LOG_DIR:-/var/log/${SERVICE_NAME}}"
SYSTEMD_UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"
DEPLOY_BINARY_PATH="${DEPLOY_BIN_DIR}/${SERVICE_NAME}"

LOCAL_ENV_FILE="${LOCAL_ENV_FILE:-}"
LOCAL_SYSTEMD_FILE="${LOCAL_SYSTEMD_FILE:-}"
HEALTHCHECK_URL="${HEALTHCHECK_URL:-http://127.0.0.1:8080/healthz}"
HEALTHCHECK_RETRIES="${HEALTHCHECK_RETRIES:-12}"
HEALTHCHECK_INTERVAL_SEC="${HEALTHCHECK_INTERVAL_SEC:-5}"

echo "[1/6] preparing local deployment directories under ${DEPLOY_DIR}"
mkdir -p "${DEPLOY_BIN_DIR}" "${DEPLOY_CONF_DIR}" "${DEPLOY_RELEASE_DIR}" "${DEPLOY_BACKUP_DIR}"
sudo mkdir -p "${DEPLOY_LOG_DIR}"

echo "[2/6] backing up previous binary if present"
if [[ -f "${DEPLOY_BINARY_PATH}" ]]; then
  cp "${DEPLOY_BINARY_PATH}" "${DEPLOY_BACKUP_DIR}/${SERVICE_NAME}-$(date +%Y%m%d%H%M%S)"
fi

echo "[3/6] copying binary into place"
cp "${LOCAL_BINARY}" "${DEPLOY_BINARY_PATH}"
chmod +x "${DEPLOY_BINARY_PATH}"

if [[ -n "${LOCAL_ENV_FILE}" ]]; then
  echo "[4/6] uploading runtime config"
  cp "${LOCAL_ENV_FILE}" "${DEPLOY_CONF_DIR}/config.env"
fi

if [[ -n "${LOCAL_SYSTEMD_FILE}" ]]; then
  echo "[4/6] installing systemd unit"
  sudo cp "${LOCAL_SYSTEMD_FILE}" "${SYSTEMD_UNIT_PATH}"
fi

echo "[5/6] restarting service"
sudo systemctl daemon-reload
sudo systemctl enable "${SERVICE_NAME}.service"
sudo systemctl restart "${SERVICE_NAME}.service"

echo "[6/6] verifying health endpoint ${HEALTHCHECK_URL}"
for ((i=1; i<=HEALTHCHECK_RETRIES; i++)); do
  if curl -fsS "${HEALTHCHECK_URL}"; then
    echo
    echo "deployment finished successfully"
    exit 0
  fi
  echo "health check attempt ${i}/${HEALTHCHECK_RETRIES} failed, retrying in ${HEALTHCHECK_INTERVAL_SEC}s"
  sleep "${HEALTHCHECK_INTERVAL_SEC}"
done

echo "deployment failed: health check did not pass"
sudo systemctl status "${SERVICE_NAME}.service" --no-pager || true
exit 1
