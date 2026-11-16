#!/usr/bin/env bash
# Shared only by packaged Linux deployment scripts.
deploy_error() { printf 'OpsView: %s\n失败阶段：%s\n安装日志：%s\n' "$*" "${OPSVIEW_STAGE:-环境检查}" "${OPSVIEW_INSTALL_LOG:-未创建}" >&2; exit 1; }

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
  local _attempt response url stable=0
  url=$(/opt/opsview/bin/opsview runtime-config health-url) || return 1
  for _attempt in {1..30}; do
    if systemctl is-active --quiet opsview-panel.service && systemctl is-active --quiet opsview-executor.service && [ -S /run/opsview/executor.sock ]; then
      # Only loopback health checks accept the initial self-signed certificate.
      if response=$(curl --noproxy '*' --silent --fail --insecure --connect-timeout 1 --max-time 2 "$url") &&
        [[ "$response" =~ \"status\"[[:space:]]*:[[:space:]]*\"ok\" ]]; then
        stable=$((stable+1))
        if [ "$stable" -ge 3 ]; then return 0; fi
      else stable=0
      fi
    else stable=0
    fi
    printf '等待服务健康检查：%s/30（连续通过 %s/3）\n' "$_attempt" "$stable"
    sleep 1
  done
  return 1
}

# stdout/stderr carry diagnostics only. FD 3 bypasses the log for one-time credentials.
setup_install_log() {
  [ -z "${OPSVIEW_INSTALL_LOG:-}" ] || return 0
  install -d -o root -g root -m 0700 /var/log/opsview
  OPSVIEW_INSTALL_LOG="/var/log/opsview/install-$(date -u +%Y%m%dT%H%M%SZ)-$$.log"
  export OPSVIEW_INSTALL_LOG
  (umask 077; : > "$OPSVIEW_INSTALL_LOG")
  chmod 0600 "$OPSVIEW_INSTALL_LOG"
  exec 3>&1
  exec > >(tee -a "$OPSVIEW_INSTALL_LOG") 2>&1
}
stage_start() { OPSVIEW_STAGE="$1/8 $2"; printf '\n[%s] %s 开始\n' "$OPSVIEW_STAGE" "$(date '+%F %T %z')"; }
stage_done() { printf '[%s] %s 完成\n' "$OPSVIEW_STAGE" "$(date '+%F %T %z')"; }
install_failure() { local code=$?; printf '\n失败阶段：%s，退出码：%s\n安装日志：%s\n' "${OPSVIEW_STAGE:-环境检查}" "$code" "${OPSVIEW_INSTALL_LOG:-未创建}" >&2; exit "$code"; }

check_ov_conflict() {
  local candidate target
  candidate=$(command -v ov || true)
  for candidate in "$candidate" /usr/local/bin/ov; do
    [ -n "$candidate" ] || continue
    if [ -e "$candidate" ] || [ -L "$candidate" ]; then
      target=$(readlink -f "$candidate" || true)
      [ "$target" = /opt/opsview/bin/opsview ] || deploy_error "已有非 OpsView 的 ov 命令：$candidate，请先解决命令冲突。"
    fi
  done
}
