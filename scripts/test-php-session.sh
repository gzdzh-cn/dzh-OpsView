#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p dist/linux-tests
(cd server && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o ../dist/linux-tests/executor.test ./internal/executor)
# Distribution PHP verifies Session protocol behavior; catalog versions retain
# their independent native build and systemd acceptance requirements.
docker run --rm --platform linux/amd64 \
  -e OPSVIEW_PHP_SESSION_SMOKE=true -e DEBIAN_FRONTEND=noninteractive \
  -v "$PWD/dist/linux-tests/executor.test:/tests/executor.test:ro" \
  ubuntu:24.04 sh -ec '
    apt-get update -qq
    apt-get install -y -qq --no-install-recommends php8.3-cli php8.3-fpm php8.3-memcached memcached sasl2-bin libsasl2-modules
    /tests/executor.test -test.run "^TestPHPSessionMemcachedSmoke$" -test.v -test.timeout=3m
  '
