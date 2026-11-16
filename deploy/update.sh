#!/usr/bin/env bash
set +x
set -Eeuo pipefail
umask 077
release_root="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=deploy/common.sh
. "$release_root/deploy/common.sh"
deploy_preflight
setup_install_log
trap install_failure ERR
public_host=''
while [ "$#" -gt 0 ]; do
  case "$1" in --public-host) [ "$#" -ge 2 ] || deploy_error '--public-host 缺少地址'; public_host=$2; shift 2;; *) deploy_error "未知参数：$1";; esac
done
[ -x /opt/opsview/bin/opsview ] && [ -d /var/lib/opsview/panel ] || deploy_error '未找到已有安装，请先运行安装程序。'
# Share the executor/ov configuration lock before checking the reconnect transaction.
command -v flock >/dev/null 2>&1 || deploy_error '缺少 flock，无法锁定运行配置。'
install -d -o root -g root -m 0755 /etc/opsview
exec 9>/etc/opsview/panel.json.lock
flock -w 5 9 || deploy_error '其他进程正在修改面板访问设置，请稍后重试。'
[ ! -e /var/lib/opsview/managed/panel-pending.json ] || deploy_error '面板访问设置等待重新连接确认，请先确认或执行 ov recover-access --all。'
stage_start 5 预检及备份现有服务
ensure_dependencies
check_ov_conflict
for binary in opsview opsview-executor opsview-plugin-worker opsview-pack opsview-install.sh; do [ -f "$release_root/bin/$binary" ] || deploy_error "发布文件缺失：$binary"; done
# The release binary understands legacy ExecStart; never remove the user's drop-ins.
"$release_root/bin/opsview" runtime-config check-overrides
backup_root="/var/lib/opsview/updates/$(date -u +%Y%m%dT%H%M%SZ)-$$"
install -d -m 0700 "$backup_root"
backup_complete=false
rollback() {
  local code=$?
  trap - ERR
  systemctl stop opsview-panel.service opsview-executor.service || true
  if [ "$backup_complete" = true ]; then
    mv /var/lib/opsview/panel "$backup_root/failed-panel"
    cp -a "$backup_root/panel" /var/lib/opsview/panel
    rm -rf /opt/opsview/bin
    cp -a "$backup_root/bin" /opt/opsview/bin
    cp -a "$backup_root/units/opsview-panel.service" "$backup_root/units/opsview-executor.service" /etc/systemd/system/
    if [ -f "$backup_root/panel.json" ]; then cp -a "$backup_root/panel.json" /etc/opsview/panel.json; else rm -f /etc/opsview/panel.json; fi
    rm -f /usr/local/bin/ov
    if [ -L "$backup_root/ov" ] || [ -e "$backup_root/ov" ]; then cp -a "$backup_root/ov" /usr/local/bin/ov; fi
    systemctl daemon-reload
  fi
  systemctl reset-failed opsview-executor.service opsview-panel.service || true
  systemctl start opsview-executor.service opsview-panel.service || true
  printf '更新失败（阶段 %s，退出码 %s），已尝试回退。备份与失败现场：%s\n安装日志：%s\n' "$OPSVIEW_STAGE" "$code" "$backup_root" "$OPSVIEW_INSTALL_LOG" >&2
  exit "$code"
}
trap rollback ERR
systemctl stop opsview-panel.service opsview-executor.service
cp -a /var/lib/opsview/panel "$backup_root/panel"
cp -a /opt/opsview/bin "$backup_root/bin"
install -d -m 0700 "$backup_root/units"
cp -a /etc/systemd/system/opsview-panel.service /etc/systemd/system/opsview-executor.service "$backup_root/units/"
if [ -f /etc/opsview/panel.json ]; then cp -a /etc/opsview/panel.json "$backup_root/panel.json"; fi
if [ -e /usr/local/bin/ov ] || [ -L /usr/local/bin/ov ]; then cp -a /usr/local/bin/ov "$backup_root/ov"; fi
backup_complete=true
stage_done
stage_start 6 更新程序并保留管理员
install -m 0755 "$release_root"/bin/* /opt/opsview/bin/
install -d -o root -g root -m 0755 /etc/opsview
config_args=(init)
[ -z "$public_host" ] || config_args+=(--public-host "$public_host")
/opt/opsview/bin/opsview runtime-config "${config_args[@]}"
install -m 0644 "$release_root/deploy/opsview-panel.service" "$release_root/deploy/opsview-executor.service" /etc/systemd/system/
ln -sfn /opt/opsview/bin/opsview /usr/local/bin/ov
systemctl daemon-reload
stage_done
stage_start 7 启动和健康检查
systemctl start opsview-executor.service opsview-panel.service
wait_for_panel || { echo '健康检查失败，正在恢复旧版本。' >&2; false; }
stage_done
trap - ERR
stage_start 8 完成
stage_done
printf '更新成功。回退备份：%s\n安装日志：%s\n' "$backup_root" "$OPSVIEW_INSTALL_LOG"
