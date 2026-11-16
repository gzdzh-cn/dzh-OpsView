#!/usr/bin/env bash
# This test operates only on an ephemeral GitHub Ubuntu runner.
set -euo pipefail
[ "${GITHUB_ACTIONS:-}" = true ] && [ "$(id -u)" -eq 0 ] || { echo '仅在隔离 CI root 主机运行此测试。' >&2; exit 1; }
cd "$(dirname "$0")/.."
[ ! -e /opt/opsview ] && [ ! -e /var/lib/opsview ] || { echo 'CI 主机必须没有 OpsView 安装。' >&2; exit 1; }
# All credential-bearing output is captured in Python memory, never in files or Actions output.
python3 scripts/test-deployment.py
