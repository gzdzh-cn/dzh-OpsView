#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [ ! -d web/node_modules ]; then (cd web && npm ci); fi
make assets
(cd server && go run ./cmd/opsview --dev --data ../.opsview) &
panel_pid=$!
cleanup() { kill "$panel_pid" 2>/dev/null || true; }
trap cleanup EXIT INT TERM
cd web
npm run dev
