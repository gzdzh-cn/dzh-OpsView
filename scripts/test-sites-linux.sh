#!/usr/bin/env bash
set -euo pipefail
[ "${GITHUB_ACTIONS:-}" = true ] || { echo 'Disposable GitHub Linux host required' >&2; exit 1; }
sudo apt-get update -qq
sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends nginx php8.3-fpm acl ufw
sudo systemctl stop nginx php8.3-fpm
mkdir -p dist/tests
(cd server && go test -c -o ../dist/tests/sites.test ./internal/executor)
run_acceptance() {
  sudo env GITHUB_ACTIONS=true OPSVIEW_SITE_SMOKE=true OPSVIEW_RUNTIME_SMOKE=true OPSVIEW_SITE_FIREWALL_SMOKE=true dist/tests/sites.test -test.run '^TestLinux(WebsiteCreation|SitePermissionRollback|SitePortOwnership)$' -test.v -test.timeout=10m || return
  # Identity cleanup scans the whole filesystem. A dedicated systemd host keeps
  # actual deletion coverage deterministic without excluding runner data from
  # the product's conservative ownership checks or extending timeouts forever.
  docker build --build-arg "APT_MIRROR=${OPSVIEW_TEST_APT_MIRROR:-}" -t opsview-site-runtime:acceptance - < scripts/site-runtime.Dockerfile || return
  container_id=$(docker run -d --privileged --cgroupns=private --tmpfs /run --tmpfs /run/lock --tmpfs /tmp \
    -v "$PWD/dist/tests/sites.test:/tests/sites.test:ro" \
    -e GITHUB_ACTIONS=true -e OPSVIEW_RUNTIME_SMOKE=true -e OPSVIEW_NODE_RUNTIME_SMOKE=true \
    opsview-site-runtime:acceptance) || return
  trap 'docker rm -f "$container_id" >/dev/null' EXIT
  for attempt in $(seq 1 60); do
    if docker exec "$container_id" test -d /run/systemd/system; then break; fi
    sleep 1
  done
  docker exec "$container_id" test -d /run/systemd/system || return
  docker exec "$container_id" /tests/sites.test -test.run '^TestLinuxSite(RuntimePolicy|AccountCleanupBudget)$' -test.v -test.timeout=6m
}
if run_acceptance 2>&1 | tee dist/tests/sites-acceptance.log; then exit 0; fi
python3 - <<'PYTHON'
from pathlib import Path
lines = Path('dist/tests/sites-acceptance.log').read_text(errors='replace').splitlines()
for i, line in enumerate(lines):
    if '--- FAIL:' not in line:
        continue
    name = line.split('--- FAIL:', 1)[1].strip().split()[0]
    start = next((j for j, value in enumerate(lines) if value.strip() == '=== RUN   ' + name), max(0, i - 8))
    end = next((j for j in range(start + 1, i) if lines[j].startswith('=== RUN')), i)
    detail = '\n'.join(lines[start:end][-12:] + [line])[-3000:]
    detail = detail.replace('%', '%25').replace('\r', '%0D').replace('\n', '%0A')
    print(f'::error title=Website acceptance failure::{detail}')
PYTHON
exit 1
