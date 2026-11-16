#!/usr/bin/env bash
# Public GitHub bootstrap. The target machine does not need Go, Node.js or Git.
set -euo pipefail
umask 077

OPSVIEW_REPOSITORY=gzdzh-cn/dzh-OpsView
OPSVIEW_ASSET=opsview-linux-amd64.tar.gz

die() { printf 'OpsView: %s\n' "$*" >&2; exit 1; }

usage() {
  echo '用法：bash install.sh [--version v0.1.0] [--update]'
  echo '默认安装 GitHub 最新正式 Release；--update 更新已有安装并在失败时回退。'
}

validate_version() {
  [[ "$1" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9]+([.-][a-zA-Z0-9]+)*)?$ ]]
}

check_host() {
  [ "$(uname -s)" = Linux ] || die '仅支持 Linux。'
  [ "$(id -u)" -eq 0 ] || die '请使用 root 或 sudo bash install.sh 执行。'
  [ "$(uname -m)" = x86_64 ] || die '当前发布包仅支持 x86_64 / amd64。'
  [ -r /etc/os-release ] || die '缺少 /etc/os-release。'
  # shellcheck source=/dev/null
  . /etc/os-release
  case "${ID:-}:${VERSION_ID:-}" in
    ubuntu:22.04|ubuntu:24.04|debian:12|debian:13) ;;
    *) die '支持 Ubuntu 22.04/24.04 和 Debian 12/13。' ;;
  esac
  if ! command -v systemctl >/dev/null || [ ! -d /run/systemd/system ]; then
    die '需要以 systemd 启动的 Linux 主机，普通 Docker 容器不可直接安装。'
  fi
}

download() {
  local url=$1 destination=$2
  if command -v curl >/dev/null 2>&1; then
    curl --fail --location --silent --show-error --retry 3 --connect-timeout 15 --max-time 600 \
      --proto '=https' --proto-redir '=https' --output "$destination" "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget --https-only --quiet --tries=3 --timeout=60 --output-document="$destination" "$url"
  else
    die '需要 curl 或 wget，请先安装其中之一。'
  fi
}

resolve_version() {
  local directory=$1 version
  download "https://api.github.com/repos/$OPSVIEW_REPOSITORY/releases/latest" "$directory/release.json" || die '无法获取最新 Release，请检查 GitHub 连通性或使用 --version 指定已发布版本。'
  version=$(sed -n 's/^[[:space:]]*"tag_name":[[:space:]]*"\([^"]*\)"[,]\{0,1\}[[:space:]]*$/\1/p' "$directory/release.json")
  validate_version "$version" || die 'GitHub 返回了无效版本号。'
  printf '%s\n' "$version"
}

verify_package() {
  local directory=$1 expected count entry type
  count=$(awk -v name="$OPSVIEW_ASSET" '$2 == name || $2 == "*" name { n++ } END { print n+0 }' "$directory/SHA256SUMS")
  [ "$count" = 1 ] || die 'SHA256SUMS 缺少唯一的发布包记录。'
  expected=$(awk -v name="$OPSVIEW_ASSET" '$2 == name || $2 == "*" name { print $1 }' "$directory/SHA256SUMS")
  [[ "$expected" =~ ^[a-fA-F0-9]{64}$ ]] || die '发布包摘要格式错误。'
  (cd "$directory" && printf '%s  %s\n' "$expected" "$OPSVIEW_ASSET" | sha256sum --check --status) || die 'SHA-256 校验失败，安装已停止。'
  # Only regular files and directories inside the expected release root may extract.
  tar -tzf "$directory/$OPSVIEW_ASSET" > "$directory/archive.list" || die '无法读取发布归档。'
  while IFS= read -r entry; do
    entry=${entry%/}
    case "$entry" in
      opsview-linux-amd64|opsview-linux-amd64/|opsview-linux-amd64/*) ;;
      *) die '发布归档包含非法路径。' ;;
    esac
    case "/$entry/" in */../*|*/./*|*//*) die '发布归档包含越界路径。' ;; esac
  done < "$directory/archive.list"
  tar -tvzf "$directory/$OPSVIEW_ASSET" > "$directory/archive.types" || die '无法检查发布归档。'
  while IFS= read -r entry; do
    type=${entry:0:1}
    [ "$type" = - ] || [ "$type" = d ] || die '发布归档包含链接或特殊文件。'
  done < "$directory/archive.types"
}

bootstrap_main() {
  local version='' mode=install directory base tool
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --version) [ "$#" -ge 2 ] || die '--version 缺少版本号。'; version=$2; validate_version "$version" || die '版本号应为 v0.1.0 或 v0.1.0-rc.1。'; shift 2 ;;
      --update) mode=update; shift ;;
      -h|--help) usage; return ;;
      *) usage >&2; die "未知参数：$1" ;;
    esac
  done
  check_host
  for tool in tar gzip sha256sum mktemp awk sed flock; do
    command -v "$tool" >/dev/null || die "缺少基础命令 $tool，请安装 tar gzip coreutils util-linux。"
  done
  # Shared by install/update entry points. Held through the child deploy process.
  exec 9>/run/lock/opsview-install.lock
  flock --nonblock 9 || die '另一个 OpsView 安装或更新正在运行。'
  export OPSVIEW_DEPLOY_LOCKED=1
  if [ "$mode" = install ]; then
    if [ -e /opt/opsview/bin/opsview ] || [ -e /var/lib/opsview/panel/opsview.db ] || [ -e /etc/systemd/system/opsview-panel.service ]; then
      die '检测到已有安装，请使用 --update；不会覆盖已有数据。'
    fi
  else
    [ -x /opt/opsview/bin/opsview ] || die '未检测到已有安装，请移除 --update。'
  fi
  directory=$(mktemp -d /tmp/opsview-install.XXXXXXXX)
  # shellcheck disable=SC2064 # Freeze the local temp path before this function returns.
  trap "rm -rf -- $(printf '%q' "$directory")" EXIT
  [ -n "$version" ] || version=$(resolve_version "$directory")
  base="https://github.com/$OPSVIEW_REPOSITORY/releases/download/$version"
  printf '正在下载 OpsView %s（%s）…\n' "$version" "$mode"
  download "$base/SHA256SUMS" "$directory/SHA256SUMS" || die '无法下载 SHA256SUMS；请确认版本已发布且仓库公开。'
  download "$base/$OPSVIEW_ASSET" "$directory/$OPSVIEW_ASSET" || die '发布包下载失败。'
  verify_package "$directory"
  tar --extract --gzip --no-same-owner --no-same-permissions --file "$directory/$OPSVIEW_ASSET" --directory "$directory"
  bash "$directory/opsview-linux-amd64/deploy/$mode.sh"
}

if [[ "${BASH_SOURCE[0]}" = "$0" ]]; then
  bootstrap_main "$@"
fi
