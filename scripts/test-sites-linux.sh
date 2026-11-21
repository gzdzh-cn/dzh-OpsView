#!/usr/bin/env bash
set -euo pipefail
[ "${GITHUB_ACTIONS:-}" = true ] || { echo 'Disposable GitHub Linux host required' >&2; exit 1; }
sudo apt-get update -qq
sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends nginx php8.3-fpm
sudo systemctl stop nginx php8.3-fpm
mkdir -p dist/tests
(cd server && go test -c -o ../dist/tests/sites.test ./internal/executor)
if sudo env GITHUB_ACTIONS=true OPSVIEW_SITE_SMOKE=true dist/tests/sites.test -test.run '^TestLinuxWebsiteCreation$' -test.v -test.timeout=5m 2>&1 | tee dist/tests/sites-acceptance.log; then exit 0; fi
python3 - <<'PYTHON'
from pathlib import Path
detail = Path('dist/tests/sites-acceptance.log').read_text(errors='replace')[-3000:]
detail = detail.replace('%', '%25').replace('\r', '%0D').replace('\n', '%0A')
print(f'::error title=Website acceptance failure::{detail}')
PYTHON
exit 1
