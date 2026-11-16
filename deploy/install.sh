#!/usr/bin/env bash
set -euo pipefail
umask 077
release_root="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=deploy/common.sh
. "$release_root/deploy/common.sh"
deploy_preflight
for binary in opsview opsview-executor opsview-plugin-worker opsview-pack; do
  if [ ! -x "$release_root/bin/$binary" ]; then echo "缺少发布文件 $binary" >&2; exit 1; fi
done
if [ -e /opt/opsview/bin/opsview ] || [ -e /etc/systemd/system/opsview-panel.service ] || [ -e /var/lib/opsview/panel/opsview.db ]; then
  deploy_error '检测到已有安装，请使用 --update；不会覆盖已有数据。'
fi
ensure_dependencies
listeners=$(ss -H -ltn 'sport = :18790') || deploy_error '无法检查面板端口。'
[ -z "$listeners" ] || deploy_error 'TCP 18790 已被占用，请先处理端口冲突。'
getent group opsview >/dev/null || groupadd --system opsview
id opsview >/dev/null 2>&1 || useradd --system --gid opsview --home-dir /var/lib/opsview/panel --shell /usr/sbin/nologin opsview
id opsview-app >/dev/null 2>&1 || useradd --system --home-dir /var/lib/opsview/managed/apps --shell /usr/sbin/nologin opsview-app
install -d -o root -g root -m 0755 /opt/opsview/bin
install -d -o root -g root -m 0711 /var/lib/opsview
install -d -o opsview -g opsview -m 0700 /var/lib/opsview/panel
install -d -o root -g root -m 0711 /var/lib/opsview/managed
install -d -o root -g root -m 0755 /etc/opsview/trust.d
install -m 0755 "$release_root"/bin/* /opt/opsview/bin/
install -m 0644 "$release_root/deploy/opsview-panel.service" "$release_root/deploy/opsview-executor.service" /etc/systemd/system/
ln -sfn /opt/opsview/bin/opsview /usr/local/bin/opsview
systemctl daemon-reload
systemctl enable --now opsview-executor.service opsview-panel.service
if ! wait_for_panel; then
  deploy_error '服务健康检查失败。请执行 journalctl -u opsview-panel -u opsview-executor -n 100 --no-pager 排查，保留现场后通过 --update 重试。'
fi
echo
echo 'OpsView 已安装，面板及执行器健康检查通过。'
echo '访问地址：https://<服务器公网 IP>:18790'
echo '本机地址：'
for address in $(hostname -I); do
  case "$address" in *:*) printf '  https://[%s]:18790\n' "$address" ;; *) printf '  https://%s:18790\n' "$address" ;; esac
done
echo '首次使用自签名 HTTPS 证书，请核对服务器地址后继续访问。'
echo '首次初始化令牌（用于创建管理员，请妥善保管）：'
cat /var/lib/opsview/panel/setup.token
echo
echo '令牌文件：/var/lib/opsview/panel/setup.token（管理员创建后自动删除）'
echo '请在宿主机防火墙及云安全组放行 TCP 18790；已有 UFW 启用时可执行：ufw allow 18790/tcp'
echo '查看状态：systemctl status opsview-panel opsview-executor --no-pager'
