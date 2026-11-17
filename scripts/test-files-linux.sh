#!/usr/bin/env bash
set -euo pipefail
[ "${GITHUB_ACTIONS:-}" = true ] || { echo 'Disposable GitHub Linux host required' >&2; exit 1; }
mkdir -p dist/tests
(cd server && go test -c -o ../dist/tests/files.test ./internal/executor)
sudo env GITHUB_ACTIONS=true OPSVIEW_FILES_ACCEPTANCE=true dist/tests/files.test -test.run 'TestLinuxProtectedBindMountAliases|TestLinuxFileCrossFilesystemMove|TestFilesThroughUnixSocket' -test.v
