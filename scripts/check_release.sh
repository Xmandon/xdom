#!/usr/bin/env bash
set -euo pipefail

: "${BASE_URL:?BASE_URL is required}"

curl -fsS "${BASE_URL}/healthz"
echo
curl -fsS "${BASE_URL}/version"
echo
