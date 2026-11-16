#!/usr/bin/env bash
# Shared only by packaged Linux deployment scripts.
deploy_error() { printf 'OpsView: %s\n' "$*" >&2; exit 1; }

deploy_preflight() {
  if [ "$(uname -s)" != Linux ] || [ "$(uname -m)" != x86_64 ]; then deploy_error '仅支持 Linux amd64。'; fi
  [ "$(id -u)" -eq 0 ] || deploy_error '请以 root 执行。'
  # shellcheck source=/dev/null
  . /etc/os-release
  case "${ID:-}:${VERSION_ID:-}" in ubuntu:22.04|ubuntu:24.04|debian:12|debian:13) ;; *) deploy_error '不支持的 Linux 发行版。' ;; esac
  if ! command -v systemctl >/dev/null || [ ! -d /run/systemd/system ]; then deploy_error '需要正在运行 systemd 的主机。'; fi
  # Direct invocation of the packaged script uses the same lock as the bootstrap.
  if [ "${OPSVIEW_DEPLOY_LOCKED:-}" != 1 ]; then
    exec 9>/run/lock/opsview-install.lock
    flock --nonblock 9 || deploy_error '另一个安装或更新正在运行。'
  fi
}

ensure_dependencies() {
  local tool missing=false
  for tool in curl ss getent useradd groupadd runuser flock; do
    command -v "$tool" >/dev/null || missing=true
  done
  [ -s /etc/ssl/certs/ca-certificates.crt ] || missing=true
  if [ "$missing" = true ]; then
    echo '正在安装基础依赖（curl、CA 证书、账户及端口检查工具）…'
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl iproute2 passwd util-linux
  fi
}

wait_for_panel() {
  local _attempt response
  for _attempt in {1..30}; do
    if systemctl is-active --quiet opsview-panel.service && systemctl is-active --quiet opsview-executor.service; then
      # Only loopback health checks accept the initial self-signed certificate.
      if response=$(curl --noproxy '*' --silent --fail --insecure --connect-timeout 1 --max-time 2 https://127.0.0.1:18790/api/v1/health) &&
        [[ "$response" =~ \"status\"[[:space:]]*:[[:space:]]*\"ok\" ]]; then
        return 0
      fi
    fi
    sleep 1
  done
  return 1
}
