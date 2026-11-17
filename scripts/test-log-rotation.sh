#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p dist/linux-tests
(cd server && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o ../dist/linux-tests/executor.test ./internal/executor)
nginx_image=$(python3 - <<'PY'
import json
entries=json.load(open('packages/catalog/versions.json'))
print(next(e['image'] for e in entries if e['software']=='nginx' and e.get('image')))
PY
)
docker run --rm --platform linux/amd64 --network none \
  -e OPSVIEW_NGINX_LOG_SMOKE=true \
  -v "$PWD/dist/linux-tests/executor.test:/tests/executor.test:ro" \
  "$nginx_image" /tests/executor.test -test.run '^TestNginxLogReopenSmoke$' -test.v -test.timeout=1m
