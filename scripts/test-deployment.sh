#!/usr/bin/env bash
# Real systemd acceptance on an ephemeral GitHub runner; never use on a live server.
set -euo pipefail
[ "${GITHUB_ACTIONS:-}" = true ] || { echo '仅在隔离 CI 主机运行此测试。' >&2; exit 1; }
[ "$(id -u)" -eq 0 ] || { echo 'CI 部署测试需要 root。' >&2; exit 1; }
cd "$(dirname "$0")/.."
if [ -e /opt/opsview ] || [ -e /var/lib/opsview ]; then echo 'CI 主机必须没有 OpsView 安装。' >&2; exit 1; fi
release_root="$PWD/dist/opsview-linux-amd64"
test_root=$(mktemp -d)
trap 'rm -rf -- "$test_root"' EXIT
trap 'echo "CI deployment failed at line $LINENO" >&2; systemctl show opsview-panel.service opsview-executor.service -p ActiveState -p Result -p ExecMainStatus >&2' ERR

# Initialization credentials remain in root-only files, never in Actions output.
bash "$release_root/deploy/install.sh" > "$test_root/install.log" 2>&1
python3 - <<'PY'
import json, os, pwd, secrets, ssl, urllib.request
with open('/var/lib/opsview/panel/setup.token') as f:
    token = f.read().strip()
payload = json.dumps(dict(token=token, username='ci-admin', password=secrets.token_hex(24))).encode()
request = urllib.request.Request('https://127.0.0.1:18790/api/v1/auth/setup', data=payload, headers={'Content-Type': 'application/json'})
with urllib.request.urlopen(request, context=ssl._create_unverified_context()) as response:
    assert response.status == 201
assert not os.path.exists('/var/lib/opsview/panel/setup.token')
assert os.stat('/var/lib/opsview/panel/opsview.db').st_uid == pwd.getpwnam('opsview').pw_uid
PY
[ "$(systemctl show -p User --value opsview-panel.service)" = opsview ]
test -S /run/opsview/executor.sock
openssl x509 -in /var/lib/opsview/panel/panel.pem -noout -fingerprint -sha256 > "$test_root/cert-before"
if bash "$release_root/deploy/install.sh" > "$test_root/reinstall.log" 2>&1; then echo 'FAIL: duplicate install succeeded' >&2; exit 1; fi
bash "$release_root/deploy/update.sh" > "$test_root/update.log" 2>&1
echo 'PASS: real systemd install, administrator setup, repeat-install rejection and update'

# A broken panel binary must trigger rollback of binaries, metadata and units.
cp -a "$release_root" "$test_root/broken"
cp /bin/false "$test_root/broken/bin/opsview"
if bash "$test_root/broken/deploy/update.sh" > "$test_root/rollback.log" 2>&1; then echo 'FAIL: broken update succeeded' >&2; exit 1; fi
# shellcheck source=deploy/common.sh
source "$release_root/deploy/common.sh"
wait_for_panel
cmp /opt/opsview/bin/opsview "$release_root/bin/opsview"
cmp /etc/systemd/system/opsview-panel.service "$release_root/deploy/opsview-panel.service"
openssl x509 -in /var/lib/opsview/panel/panel.pem -noout -fingerprint -sha256 > "$test_root/cert-after"
cmp "$test_root/cert-before" "$test_root/cert-after"
python3 - <<'PY'
import json, ssl, urllib.request
with urllib.request.urlopen('https://127.0.0.1:18790/api/v1/auth', context=ssl._create_unverified_context()) as response:
    state = json.load(response)
assert state['setupRequired'] is False
PY
echo 'PASS: failed update restored working services, binaries, administrator state and certificate'
