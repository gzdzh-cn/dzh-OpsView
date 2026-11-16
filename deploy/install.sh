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
if [ "${OPSVIEW_BOOTSTRAP:-}" != 1 ]; then stage_start 1 环境检查; stage_done; fi
public_host=''
while [ "$#" -gt 0 ]; do
  case "$1" in --public-host) [ "$#" -ge 2 ] || deploy_error '--public-host 缺少地址'; public_host=$2; shift 2;; *) deploy_error "未知参数：$1";; esac
done
for binary in opsview opsview-executor opsview-plugin-worker opsview-pack opsview-install.sh; do
  [ -f "$release_root/bin/$binary" ] || deploy_error "缺少发布文件 $binary"
done
if [ -e /opt/opsview/bin/opsview ] || [ -e /etc/systemd/system/opsview-panel.service ] || [ -e /var/lib/opsview/panel/opsview.db ]; then
  deploy_error '检测到已有安装，请使用 --update；不会覆盖已有数据。'
fi
check_ov_conflict
stage_start 5 安装依赖及服务文件
ensure_dependencies
listeners=$(ss -H -ltn 'sport = :18790') || deploy_error '无法检查面板端口。'
[ -z "$listeners" ] || deploy_error 'TCP 18790 已被占用，请先处理端口冲突。'
getent group opsview >/dev/null || groupadd --system opsview
id opsview >/dev/null 2>&1 || useradd --system --gid opsview --home-dir /var/lib/opsview/panel --shell /usr/sbin/nologin opsview
id opsview-app >/dev/null 2>&1 || useradd --system --home-dir /var/lib/opsview/managed/apps --shell /usr/sbin/nologin opsview-app
install -d -o root -g root -m 0755 /opt/opsview/bin /etc/opsview /etc/opsview/trust.d
install -d -o root -g root -m 0711 /var/lib/opsview /var/lib/opsview/managed
install -d -o opsview -g opsview -m 0700 /var/lib/opsview/panel
install -m 0755 "$release_root"/bin/* /opt/opsview/bin/
config_args=(init)
[ -z "$public_host" ] || config_args+=(--public-host "$public_host")
/opt/opsview/bin/opsview runtime-config "${config_args[@]}"
install -m 0644 "$release_root/deploy/opsview-panel.service" "$release_root/deploy/opsview-executor.service" /etc/systemd/system/
ln -sfn /opt/opsview/bin/opsview /usr/local/bin/opsview
ln -sfn /opt/opsview/bin/opsview /usr/local/bin/ov
systemctl daemon-reload
stage_done
stage_start 6 创建管理员
# Captured only in shell memory: never exported, written to a file, or passed as an argument.
unset opsview_generated_credentials
opsview_generated_credentials=$(/opt/opsview/bin/opsview admin-init)
stage_done
stage_start 7 启动和健康检查
systemctl enable --now opsview-executor.service opsview-panel.service
wait_for_panel || deploy_error '服务健康检查失败，请检查两个服务日志；修复后通过 ov password 生成新密码。'
stage_done
stage_start 8 完成
stage_done
printf '安装日志：%s\n' "$OPSVIEW_INSTALL_LOG"
# The credential channel is intentionally outside the diagnostic tee.
printf '%s' "$opsview_generated_credentials" | /opt/opsview/bin/opsview runtime-config summary >&3
unset opsview_generated_credentials
