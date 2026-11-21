#!/usr/bin/env bash
# Runs destructive acceptance ONLY inside a new disposable Docker/systemd host.
set -euo pipefail
cd "$(dirname "$0")/.."
test_dir="$PWD/dist/uninstall-tests"
mkdir -p "$test_dir/bin" "$test_dir/deploy"
architecture=$(docker info --format '{{.Architecture}}')
case "$architecture" in aarch64|arm64) architecture=arm64;; x86_64|amd64) architecture=amd64;; *) echo 'Unsupported Docker test architecture' >&2; exit 1;; esac
for component in opsview executor plugin-worker pack; do
  binary="opsview-$component"
  [ "$component" != opsview ] || binary=opsview
  (cd server && CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" go build -trimpath -o "$test_dir/bin/$binary" "./cmd/$component")
done
(cd server && CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" go test -c -o "$test_dir/maintenance.test" ./internal/maintenance)
cp deploy/*.service "$test_dir/deploy/"
cp install.sh "$test_dir/bin/opsview-install.sh"
# Load an ordinary Redis image into the isolated daemon; never mount the host socket.
docker image inspect redis:7 >/dev/null 2>&1 || docker pull redis:7
docker image save -o "$test_dir/redis.tar" redis:7
docker build --build-arg "APT_MIRROR=${OPSVIEW_TEST_APT_MIRROR:-}" -t opsview-uninstall-test:local - < scripts/uninstall.Dockerfile
container_id=$(docker run -d --privileged --cgroupns=private --tmpfs /run --tmpfs /run/lock \
  -v "$test_dir:/tests:ro" -e OPSVIEW_UNINSTALL_LINUX=1 opsview-uninstall-test:local)
trap 'docker rm -f "$container_id" >/dev/null' EXIT
for attempt in $(seq 1 60); do
  if docker exec "$container_id" test -d /run/systemd/system; then break; fi
  sleep 1
done
docker exec "$container_id" /tests/maintenance.test -test.run '^TestLinuxPanelUninstall$' -test.v -test.timeout=8m
