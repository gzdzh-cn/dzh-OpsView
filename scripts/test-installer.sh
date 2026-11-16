#!/usr/bin/env bash
# Tests use fixture downloads; no service or host firewall is changed.
set -euo pipefail
cd "$(dirname "$0")/.."
bash -n install.sh deploy/common.sh deploy/install.sh deploy/update.sh scripts/release.sh
export COPYFILE_DISABLE=1
# shellcheck source=install.sh
source ./install.sh
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT

expect_failure() {
  local name=$1
  shift
  if ("$@") >"$fixture/output" 2>&1; then
    echo "FAIL: $name unexpectedly succeeded" >&2; exit 1
  fi
  printf 'PASS: %s\n' "$name"
}

checksum() {
  if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi
}
if [ "$(uname -s)" = Darwin ] || ! command -v sha256sum >/dev/null; then sha256sum() { shasum -a 256 "$@"; }; fi

validate_version v0.1.0
validate_version v1.2.3-rc.1
expect_failure 'reject path traversal in version' validate_version 'v1.2.3/../../main'
expect_failure 'reject shell syntax in version' validate_version 'v1.2.3;id'
expect_failure 'reject unknown installer argument' bootstrap_main --invalid
expect_failure 'reject missing version argument' bootstrap_main --version
if [ "$(uname -s)" != Linux ]; then expect_failure 'reject non-Linux host' bootstrap_main; fi

mkdir -p "$fixture/source/opsview-linux-amd64/deploy" "$fixture/download"
cat > "$fixture/source/opsview-linux-amd64/deploy/install.sh" <<'SH'
#!/usr/bin/env bash
printf installed > "$TEST_OUTPUT_PATH"
SH
cp "$fixture/source/opsview-linux-amd64/deploy/install.sh" "$fixture/source/opsview-linux-amd64/deploy/update.sh"
tar -C "$fixture/source" -czf "$fixture/download/$OPSVIEW_ASSET" opsview-linux-amd64
(cd "$fixture/download" && checksum "$OPSVIEW_ASSET" > SHA256SUMS)
verify_package "$fixture/download"
echo 'PASS: valid release checksum and archive'

cp "$fixture/download/SHA256SUMS" "$fixture/good-sums"
printf '%064d  %s\n' 0 "$OPSVIEW_ASSET" > "$fixture/download/SHA256SUMS"
expect_failure 'reject altered release archive' verify_package "$fixture/download"
cp "$fixture/good-sums" "$fixture/download/SHA256SUMS"
cat "$fixture/good-sums" >> "$fixture/download/SHA256SUMS"
expect_failure 'reject duplicate checksum entry' verify_package "$fixture/download"
echo '0000000000000000000000000000000000000000000000000000000000000000  other.tar.gz' > "$fixture/download/SHA256SUMS"
expect_failure 'reject missing checksum entry' verify_package "$fixture/download"

ln -s /etc "$fixture/source/opsview-linux-amd64/escape"
tar -C "$fixture/source" -czf "$fixture/download/$OPSVIEW_ASSET" opsview-linux-amd64
(cd "$fixture/download" && checksum "$OPSVIEW_ASSET" > SHA256SUMS)
expect_failure 'reject archive symlink' verify_package "$fixture/download"
rm "$fixture/source/opsview-linux-amd64/escape"
echo bad > "$fixture/source/outside"
tar -C "$fixture/source" -czf "$fixture/download/$OPSVIEW_ASSET" outside
(cd "$fixture/download" && checksum "$OPSVIEW_ASSET" > SHA256SUMS)
expect_failure 'reject unexpected archive root' verify_package "$fixture/download"
tar -C "$fixture/source" -czf "$fixture/download/$OPSVIEW_ASSET" opsview-linux-amd64
(cd "$fixture/download" && checksum "$OPSVIEW_ASSET" > SHA256SUMS)

(
  download() {
    [ "$1" = "https://api.github.com/repos/$OPSVIEW_REPOSITORY/releases/latest" ] || return 1
    printf '{\n  "tag_name": "v0.1.0",\n  "draft": false\n}\n' > "$2"
  }
  [ "$(resolve_version "$fixture")" = v0.1.0 ]
)
echo 'PASS: latest resolves to concrete version'
bad_latest() { download() { printf '{"message":"Not Found"}\n' > "$2"; }; resolve_version "$fixture"; }
expect_failure 'reject malformed release response' bad_latest
failed_latest() { download() { return 22; }; resolve_version "$fixture"; }
expect_failure 'reject failed release download' failed_latest

# Exercise both real downloader branches without connecting to the network.
(
  curl() { printf '%s\n' "$@" > "$fixture/curl.args"; }
  download 'https://example.invalid/asset' "$fixture/asset"
  grep -Fx -- '--fail' "$fixture/curl.args" >/dev/null
  grep -Fx -- '=https' "$fixture/curl.args" >/dev/null
)
echo 'PASS: curl fails HTTP errors and enforces HTTPS'
(
  # shellcheck disable=SC2317 # Invoked indirectly by download().
  # shellcheck disable=SC2329 # download() invokes this command lookup override.
  command() { if [ "${1:-}" = -v ] && [ "${2:-}" = curl ]; then return 1; fi; builtin command "$@"; }
  wget() { printf '%s\n' "$@" > "$fixture/wget.args"; }
  download 'https://example.invalid/asset' "$fixture/asset"
  grep -Fx -- '--https-only' "$fixture/wget.args" >/dev/null
)
echo 'PASS: wget fallback enforces HTTPS'

if [ "$(uname -s)" = Linux ] && [ "$(id -u)" = 0 ]; then
  export TEST_OUTPUT_PATH="$fixture/installed"
  (
    check_host() { :; }
    download() {
      printf '%s\n' "$1" >> "$fixture/urls"
      case "$1" in
        */releases/latest) printf '{\n "tag_name": "v0.1.0"\n}\n' > "$2" ;;
        */v0.1.0/SHA256SUMS) cp "$fixture/download/SHA256SUMS" "$2" ;;
        */v0.1.0/opsview-linux-amd64.tar.gz) cp "$fixture/download/$OPSVIEW_ASSET" "$2" ;;
        *) return 22 ;;
      esac
    }
    bootstrap_main
  )
  [ "$(cat "$TEST_OUTPUT_PATH")" = installed ]
  [ "$(grep -c /download/v0.1.0/ "$fixture/urls")" = 2 ]
  echo 'PASS: full bootstrap pins both assets to resolved tag and runs installer'
  rm "$TEST_OUTPUT_PATH"
  broken_bootstrap() {
    check_host() { :; }
    download() { case "$1" in */SHA256SUMS) printf '%064d  %s\n' 0 "$OPSVIEW_ASSET" > "$2" ;; *) cp "$fixture/download/$OPSVIEW_ASSET" "$2" ;; esac; }
    bootstrap_main --version v0.1.0
  }
  expect_failure 'full bootstrap stops before execution on checksum mismatch' broken_bootstrap
  [ ! -e "$TEST_OUTPUT_PATH" ]
fi
echo 'Installer checks passed.'
