#!/usr/bin/env bash
set -euo pipefail

: "${DEPLOY_HOST:?DEPLOY_HOST is required}"
: "${DEPLOY_USER:?DEPLOY_USER is required}"
: "${DEPLOY_DIR:?DEPLOY_DIR is required}"
: "${SERVICE_NAME:?SERVICE_NAME is required}"
: "${LOCAL_BINARY:?LOCAL_BINARY is required}"

REMOTE_BIN_DIR="${DEPLOY_DIR}/bin"
REMOTE_CONF_DIR="${DEPLOY_DIR}/conf"

ssh "${DEPLOY_USER}@${DEPLOY_HOST}" "mkdir -p '${REMOTE_BIN_DIR}' '${REMOTE_CONF_DIR}'"
scp "${LOCAL_BINARY}" "${DEPLOY_USER}@${DEPLOY_HOST}:${REMOTE_BIN_DIR}/${SERVICE_NAME}"
ssh "${DEPLOY_USER}@${DEPLOY_HOST}" "chmod +x '${REMOTE_BIN_DIR}/${SERVICE_NAME}'"

if [[ -n "${LOCAL_ENV_FILE:-}" ]]; then
  scp "${LOCAL_ENV_FILE}" "${DEPLOY_USER}@${DEPLOY_HOST}:${REMOTE_CONF_DIR}/config.env"
fi

ssh "${DEPLOY_USER}@${DEPLOY_HOST}" "sudo systemctl daemon-reload && sudo systemctl restart '${SERVICE_NAME}.service'"
echo "deployment finished"
