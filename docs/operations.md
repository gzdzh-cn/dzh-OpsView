# 运维手册

## 目录及凭据

| 路径 | 内容与权限 |
| --- | --- |
| `/opt/opsview/bin` | root 安装的四个二进制及受控更新脚本 |
| `/var/lib/opsview/panel` | opsview 用户 0700，SQLite、面板证书、配置备份（未初始化的旧安装还包含令牌） |
| `/var/lib/opsview/panel/master.key` | 面板凭据与配置备份主密钥，单独离线保存 |
| `/var/lib/opsview/managed` | root 管理的业务目录，实例、站点、应用、备份、状态 |
| `/var/lib/opsview/managed/executor.key` | 执行器状态密钥，单独离线保存 |
| `/var/lib/opsview/managed/backup.key` | 业务备份密钥，单独离线保存 |
| `/etc/opsview/panel.json` | root:root 0644，监听 IP、端口、对外主机，无密码 |
| `/var/log/opsview` | root 0700，安装日志 0600，不含一次性密码 |
| `/etc/opsview/trust.d` | root 管理的 Ed25519 公钥 |
| `/run/opsview/executor.sock` | root:opsview，0660 |

这些密钥用途不同。仅复制 `.ovb` 或 `.ovp` 文件不能恢复。完整灾难恢复还需要对应密钥、执行器状态、实例定义与业务文件；面板配置备份不等于整机备份。

## 建站与应用

1. 在软件页面选择版本与安装方式，安装 Nginx。原生安装至少预留 6 GiB，容器安装至少预留 2 GiB；实际容量随数据量增加。
2. 创建静态站点，绑定已解析到本机的域名。文件页面选择该站点上传 ZIP 并解压，或上传文件。
3. 应用进程先保存配置，在文件页面上传已构建程序/Node 文件，必要时 chmod 0755，再重新部署。工作目录固定在受管理应用目录；首版不在线编译源码。
4. 代理站点可以选择应用端口，或填写容器已发布到 loopback 的 HTTP 地址。
5. HTTP-01 申请证书要求公网域名、80 端口可达和同意 CA 条款。自动续期每天检查一次，剩余不足 30 天时提交任务。DNS-01 扩展尚未提供。
6. 受控高级配置支持 `client_max_body_size`、`proxy_read_timeout`、`proxy_connect_timeout`、`proxy_send_timeout`、`send_timeout`、`gzip`；禁止通过编辑器引入任意 include 或根目录。
7. 配置历史展示已生成配置和近期历史。发现外部变更后，在检查内容后选择接受差异重新生成；该操作保留外部配置为历史。

Nginx 多版本只能同时启用一个入口。新版本默认停止；切换时先停旧实例，启动新实例，再将站点关联改为新实例并应用。这会有切换窗口，首版不承诺无停机升级。

## 数据库及恢复

MySQL 与 PostgreSQL 的实例、数据目录、端口及原生用户独立。创建数据库时创建专用用户。默认来源为 `127.0.0.1`；远程来源必须是单个 IP 或 CIDR，MySQL IPv6 使用单个地址。

远程访问需要已启用且兼容的 UFW，创建规则后在安全页面 120 秒内确认。最后一条关联远程规则撤回时，执行器尝试恢复 loopback 绑定。云安全组需另外检查。应用使用不同容器网络时，不能把容器内 `127.0.0.1` 当作宿主机数据库地址。

“访问与密码”修改专用用户的密码及来源，不支持直接修改数据库名、用户名或所在实例。建库任务若已完成 SQL 步骤但远程网络步骤失败，可使用此入口重新应用访问设置。失败现场保留，先核对任务日志再操作。

SQL 导入使用对应实例的 `imports/` 目录，网页上传/读取上限为 10 MiB；大文件可由主机管理员传入此目录后提交导入。导出使用对应实例自带客户端。PostgreSQL 导出是含 `database.dump` 和 `roles.sql` 的 tar.gz，MySQL 为 SQL 文件。导入文件属于受信任的数据库管理输入，不运行宿主机 Shell。

数据库备份恢复只写入新的数据库名，创建同名专用用户、复用原密码并默认限制为本机，成功后登记为新的受管理数据库。新数据库尚需检查应用兼容性后切换连接。PostgreSQL 角色备份作为灾难恢复参考保留；首版不会把整个角色清单自动恢复到已有服务器，以免覆盖其他用户。

迁移前必须停止应用写入。MySQL 自动迁移限定 8.4 LTS 内，PostgreSQL 限定 public schema 的表并禁止降大版本；验证表及行数后切换面板关联。它不能替代应用层校验。源实例和库保留，执行器记录 migration-source 以防误卸载；首版清理源快照需本机人工审核，未提供自动清理按钮。

## 备份及面板配置恢复

业务备份使用认证分块加密与 SHA-256。S3 模式保留本地副本，同时上传远端；恢复本地文件缺失时从 S3 取回，并对照执行器加密状态内的历史摘要校验。文件备份为在线文件复制，不保证应用事务一致性，且拒绝符号链接和特殊文件。MySQL 在线一致性依赖事务表与维护约定。

面板配置可在设置页面备份和下载，或使用本机命令：

```bash
sudo opsview backup-config
sudo opsview restore-config --file /var/lib/opsview/panel/backups/EXAMPLE.ovp --output /var/lib/opsview/panel-restored
```

`restore-config` 读取当前数据目录中的 `master.key`，认证解密后恢复 SQLite、面板证书和运行配置快照到新目录，检查 SQLite 完整性，并使旧会话失效。新目录必须尚不存在。默认不会切换正在运行的面板。`panel.json` 快照不会自动覆盖 `/etc/opsview/panel.json`，需人工核对后切换。写入运行配置时使用 `install -o root -g root -m 0644`，确保仍由 root 管理且面板可以读取。

验证新目录后停止面板，保留旧目录，将新目录切换为 `/var/lib/opsview/panel`，确保 owner 为 opsview、目录 0700，再启动面板。不要在运行中替换 SQLite 数据库，也不要遗漏 WAL 文件。恢复后重启的任务标为中断，核对执行器现场后再重试。

## 升级和维护

已按 README 下载 bootstrap 时，可以直接更新：

```bash
sudo ov update
sudo ov update --version v0.1.1
```

也可以解压新包后，在服务器执行：

```bash
sudo bash opsview-linux-amd64/deploy/update.sh
```

更新脚本停止面板及执行器，完整备份面板目录、旧二进制、受控更新脚本、运行配置、ov 入口与 systemd 单元；替换后检查两个服务及 HTTPS 健康接口，失败时把失败数据目录移出并恢复完整旧目录、二进制和单元。备份保存于 `/var/lib/opsview/updates/`，含主密钥，须限制访问并按需离线保存。自定义 systemd 配置使用 `systemctl edit` 的 drop-in，主单元随发布包更新。与端口管理冲突的 ExecStart 覆盖会拒绝更新。版本 0.1.x 的 schema 为 1；数据库来自较新 schema 时拒绝打开。

已安装的 `opsview` 命令默认使用 `/var/lib/opsview/panel`。root 执行维护命令时，会转为 opsview 用户，避免创建 root 所属 WAL；信任公钥命令保留 root 权限。开发二进制默认使用当前目录的 `.opsview`，可用 `--data` 指定。

```bash
sudo ov                          # 中文编号菜单，非交互环境显示帮助
sudo ov info                     # 实际地址、用户名；密码不保存
sudo ov password                 # 隐藏输入，留空生成随机密码；保留 TOTP
sudo ov username new-user        # 更改用户名，旧会话失效；保留 TOTP
sudo ov reset-2fa                # 单独确认后关闭 TOTP
sudo ov recover-access          # 清空面板 IP 限制，旧会话失效
sudo ov diagnose                # 脱敏诊断输出
sudo ov port                    # 来源受限的 UFW 放行、重启及本机健康检查，失败回退
sudo ov info --public-host panel.example.com
```

`ov start` 确保执行器运行；`ov stop`、`ov restart` 只操作面板。`ov status`、`ov logs` 同时查看两个服务。密码只在生成时显示，不写入安装日志、journal、命令参数或环境变量。自动化可通过 `ov password --stdin` 传入密码。已有管理员不会被安装或更新覆盖；未初始化的旧版本可执行 `ov init`。

保留旧维护命令：`opsview reset-admin` 会同时关闭 TOTP，`opsview recover-access`、`opsview backup-config`、`opsview diagnose` 仍可使用。完整菜单和安装进度说明见 [安装教程](../安装教程.md)。

## 防火墙

面板不自动启用或重置已有 UFW，不接管未知 nftables 策略。主机管理员应先核对实际 SSH 端口、放行 SSH 和面板，再启用 UFW。面板从运行配置读取实际面板端口，拒绝覆盖保留端口和 sshd 配置端口的 deny 规则。

宿主机规则使用 UFW；Docker bridge 的 DNAT 发布端口使用 DOCKER-USER，要求兼容 iptables 后端；不接管 Docker routed/direct-routing 模式。ALLOW 规则只放行其匹配来源，不会自动拒绝所有其他来源；要实现白名单，应先为对应端口添加 deny any 并确认，再添加指定来源 allow。规则插入链头，后添加的规则先匹配。务必从独立机器验证实际可达性，IPv4 和 IPv6 分别检查。核心数据库使用 host 网络，受宿主机规则管理。

修改规则需要在 120 秒内确认，否则撤回；待确认信息保存于磁盘，执行器重启后继续检查。此机制不能代替外部网络验收。云安全组仍属外部限制，面板不能判断互联网最终可达状态。
