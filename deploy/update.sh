#!/usr/bin/env bash
set -euo pipefail
umask 077
release_root="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=deploy/common.sh
. "$release_root/deploy/common.sh"
deploy_preflight
if [ ! -x /opt/opsview/bin/opsview ] || [ ! -d /var/lib/opsview/panel ]; then deploy_error '未找到已有安装，请先运行安装程序。'; fi
ensure_dependencies
backup_root="/var/lib/opsview/updates/$(date -u +%Y%m%dT%H%M%SZ)-$$"
for binary in opsview opsview-executor opsview-plugin-worker opsview-pack; do [ -x "$release_root/bin/$binary" ] || { echo "发布文件缺失：$binary" >&2; exit 1; }; done
install -d -m 0700 "$backup_root"
backup_complete=false
rollback() {
  trap - ERR
  systemctl stop opsview-panel.service opsview-executor.service || true
  if [ "$backup_complete" = true ]; then
    mv /var/lib/opsview/panel "$backup_root/failed-panel"
    cp -a "$backup_root/panel" /var/lib/opsview/panel
    rm -rf /opt/opsview/bin
    cp -a "$backup_root/bin" /opt/opsview/bin
    cp -a "$backup_root/units/opsview-panel.service" "$backup_root/units/opsview-executor.service" /etc/systemd/system/
    systemctl daemon-reload
  fi
  systemctl reset-failed opsview-executor.service opsview-panel.service || true
  systemctl start opsview-executor.service opsview-panel.service || true
  echo "更新失败，已尝试回退。备份与失败现场：$backup_root；请检查两个 systemd 服务状态。" >&2
}
trap rollback ERR
systemctl stop opsview-panel.service opsview-executor.service
cp -a /var/lib/opsview/panel "$backup_root/panel"
cp -a /opt/opsview/bin "$backup_root/bin"
install -d -m 0700 "$backup_root/units"
cp -a /etc/systemd/system/opsview-panel.service /etc/systemd/system/opsview-executor.service "$backup_root/units/"
backup_complete=true
install -m 0755 "$release_root"/bin/* /opt/opsview/bin/
install -m 0644 "$release_root/deploy/opsview-panel.service" "$release_root/deploy/opsview-executor.service" /etc/systemd/system/
systemctl daemon-reload
systemctl start opsview-executor.service opsview-panel.service
if wait_for_panel; then trap - ERR; echo "更新成功。回退备份：$backup_root"; exit 0; fi
echo '健康检查失败，正在恢复旧版本。' >&2
false
