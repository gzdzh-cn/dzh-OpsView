# 运维手册

## 目录及凭据

| 路径 | 内容与权限 |
| --- | --- |
| `/opt/opsview/bin` | root 安装的四个二进制及受控更新脚本 |
| `/var/lib/opsview/panel` | opsview 用户 0700，SQLite、面板证书、配置备份（未初始化的旧安装还包含令牌） |
| `/var/lib/opsview/panel/master.key` | 面板凭据与配置备份主密钥，单独离线保存 |
| `/var/lib/opsview/managed` | root 管理的业务目录，实例、站点、应用、备份、状态 |
| `/var/lib/opsview/managed/log-archives` | 软件日志归档；范围目录 0700、文件 0600，仅 root 可读原始内容 |
| `/var/lib/opsview/managed/executor.key` | 执行器状态密钥，单独离线保存 |
| `/var/lib/opsview/managed/backup.key` | 业务备份密钥，单独离线保存 |
| `/etc/opsview/panel.json` | root:root 0644，监听 IP、端口、对外主机，无密码 |
| `/var/log/opsview` | root 0700，安装日志 0600，不含一次性密码 |
| `/etc/opsview/trust.d` | root 管理的 Ed25519 公钥 |
| `/run/opsview/executor.sock` | root:opsview，0660 |

这些密钥用途不同。仅复制 `.ovb` 或 `.ovp` 文件不能恢复。完整灾难恢复还需要对应密钥、执行器状态、实例定义与业务文件；面板配置备份不等于整机备份。

## 建站与应用

1. 在软件页面选择版本与安装方式，安装 Nginx。软件市场只提供原生安装，至少预留 6 GiB；实际容量随数据量增加。
2. 创建静态站点，绑定已解析到本机的域名。从网站根目录进入文件页，上传 ZIP/TAR.GZ 并解压，或上传文件。
3. 应用进程功能已下线，外部服务由管理员或 Docker 托管。旧进程、文件和历史记录保留，原应用网站继续使用已保存的外部上游。
4. 代理站点可以填写外部服务地址，或填写容器已发布到 loopback 的 HTTP 地址。
5. HTTP-01 申请证书要求公网域名、80 端口可达和同意 CA 条款。自动续期每小时检查一次，按证书剩余寿命阈值提交任务，适配短周期证书。DNS-01 扩展尚未提供。
6. 受控高级配置支持 `client_max_body_size`、`proxy_read_timeout`、`proxy_connect_timeout`、`proxy_send_timeout`、`send_timeout`、`gzip`；禁止通过编辑器引入任意 include 或根目录。
7. 配置历史展示已生成配置和近期历史。发现外部变更后，在检查内容后选择接受差异重新生成；该操作保留外部配置为历史。

Nginx 多版本只能同时启用一个入口。新版本默认停止；在实例设置的“版本切换”选择已安装版本，任务锁定入口、校验全部网站、启动目标并验证接管，成功后更新关联，失败恢复原入口。切换存在窗口，不承诺无停机升级。

## 数据库及恢复

MySQL 与 PostgreSQL 的实例、数据目录、端口及原生用户独立。创建数据库时创建专用用户。默认来源为 `127.0.0.1`；远程来源必须是单个 IP 或 CIDR，MySQL IPv6 使用单个地址。

远程访问需要已启用且兼容的 UFW，创建规则后在安全页面 120 秒内确认。最后一条关联远程规则撤回时，执行器尝试恢复 loopback 绑定。云安全组需另外检查。应用使用不同容器网络时，不能把容器内 `127.0.0.1` 当作宿主机数据库地址。

“改密”与“连接来源”分别修改账号密码和来源，不支持直接修改数据库名、用户名或所在实例。建库任务若已完成 SQL 步骤但远程网络步骤失败，可使用此入口重新应用访问设置。失败现场保留，先核对任务日志再操作。

SQL 导入使用对应实例的 `imports/` 目录，服务器文件页上传上限为 2 GiB、文本编辑 10 MiB；大文件可由主机管理员传入此目录后提交导入。导出使用对应实例自带客户端。PostgreSQL 导出是含 `database.dump` 和 `roles.sql` 的 tar.gz，MySQL 为 SQL 文件。导入文件属于受信任的数据库管理输入，不运行宿主机 Shell。

数据库备份恢复只写入新的数据库名，创建同名专用用户、生成新的随机密码并默认限制为本机，成功后登记为新的受管理数据库。新数据库尚需检查应用兼容性后切换连接。PostgreSQL 角色备份作为灾难恢复参考保留；首版不会把整个角色清单自动恢复到已有服务器，以免覆盖其他用户。

迁移前必须停止应用写入。MySQL 自动迁移限定 8.4 LTS 内，PostgreSQL 限定同大版本及 public schema 的表；验证表及行数后切换面板关联。它不能替代应用层校验。源实例和库保留，执行器记录 migration-source 以防误卸载；首版清理源快照需本机人工审核，未提供自动清理按钮。

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

更新脚本停止面板及执行器，完整备份面板目录、旧二进制、受控更新脚本、运行配置、ov 入口与 systemd 单元；替换后检查两个服务及 HTTPS 健康接口，失败时把失败数据目录移出并恢复完整旧目录、二进制和单元。备份保存于 `/var/lib/opsview/updates/`，含主密钥，须限制访问并按需离线保存。自定义 systemd 配置使用 `systemctl edit` 的 drop-in，主单元随发布包更新。与端口管理冲突的 ExecStart 覆盖会拒绝更新。v0.1.3-rc.5 的 schema 为 3（rc.1–rc.4 为 2）；数据库来自较新 schema 时拒绝打开。

已安装的 `opsview` 命令默认使用 `/var/lib/opsview/panel`。root 执行维护命令时，会转为 opsview 用户，避免创建 root 所属 WAL；信任公钥命令保留 root 权限。开发二进制默认使用当前目录的 `.opsview`，可用 `--data` 指定。

```bash
sudo ov                          # 中文编号菜单，非交互环境显示帮助
sudo ov info                     # 实际地址、用户名；密码不保存
sudo ov password                 # 隐藏输入，留空生成随机密码；保留 TOTP
sudo ov username new-user        # 更改用户名，旧会话失效；保留 TOTP
sudo ov reset-2fa                # 单独确认后关闭 TOTP
sudo ov recover-access          # 清空面板 IP 限制，旧会话失效
sudo ov diagnose                # 脱敏诊断输出
sudo ov port                    # 来源受限 UFW 放行，120 秒内从新链接确认，超时回退
sudo ov info --public-host panel.example.com
```

`ov start` 确保执行器运行；`ov stop`、`ov restart` 只操作面板。`ov status`、`ov logs` 同时查看两个服务。密码只在生成时显示，不写入安装日志、journal、命令参数或环境变量。自动化可通过 `ov password --stdin` 传入密码。已有管理员不会被安装或更新覆盖；未初始化的旧版本可执行 `ov init`。

保留旧维护命令：`opsview reset-admin` 会同时关闭 TOTP，`opsview recover-access`、`opsview backup-config`、`opsview diagnose` 仍可使用。完整菜单和安装进度说明见 [安装教程](../安装教程.md)。

## 防火墙

面板不自动启用或重置已有 UFW，不接管未知 nftables 策略。主机管理员应先核对实际 SSH 端口、放行 SSH 和面板，再启用 UFW。面板从运行配置读取实际面板端口，拒绝覆盖保留端口和 sshd 配置端口的 deny 规则。

宿主机规则使用 UFW；Docker bridge 的 DNAT 发布端口使用 DOCKER-USER，要求兼容 iptables 后端；不接管 Docker routed/direct-routing 模式。ALLOW 规则只放行其匹配来源，不会自动拒绝所有其他来源；要实现白名单，应先为对应端口添加 deny any 并确认，再添加指定来源 allow。规则插入链头，后添加的规则先匹配。务必从独立机器验证实际可达性，IPv4 和 IPv6 分别检查。核心数据库使用 host 网络，受宿主机规则管理。

修改规则需要在 120 秒内确认，否则撤回；待确认信息保存于磁盘，执行器重启后继续检查。此机制不能代替外部网络验收。云安全组仍属外部限制，面板不能判断互联网最终可达状态。

## 软件配置、站点到期与面板访问设置

实例配置仅允许编辑执行器管理的文件及参数。配置 API 返回 SHA-256 修订号，提交使用 `baseRevision`；文件外部变化时拒绝覆盖。草稿和历史使用执行器密钥加密，位于 `managed/instances/<id>/history/<file>/`。PHP 指定站点配置的历史位于 `managed/sites/<id>/history/`。编辑器返回凭据遮盖值，遮盖值不会替换原密码。

配置应用执行候选检查、历史保存、受控应用和协议探测。MySQL 静态参数重启；Redis 白名单参数动态应用并原子保存受限文件，失败恢复运行值及文件。启用 AOF 等待后台重写完成；关闭 AOF 必须确认影响。Nginx 新建实例额外分配 loopback 状态端口，应用后通过新修订探针检查工作进程接管。旧 Nginx 没有状态端口时显示不可用。

PHP 网站使用独立 FPM 单元 `opsview-site-php-<id>.service`，普通用户、Unix Socket、本机状态端点。错误日志写入各站点 `logs/php-error.log`，文件 Session 写入各站点 `sessions/`（0700），不会继承版本目录中的可写路径。网站编辑保留现有站点 ini/FPM 设置；继承 Redis Session 时同步登记依赖。PECL 使用对应版本的 phpize/php-config 构建和验证，随后重载关联活动服务。文件、Redis、本机 memcached Session 的修改先验证真实会话读写，再保存加密历史及重载；清理只处理受管目录下超过 24 小时未更新的会话文件，拒绝目录符号链接并保留活跃文件。

执行器每 15 秒采集结构化网站访问日志和检查到期。流量游标与小时汇总原子提交，按服务器时区统计；到期阻断公众访问、暂停专属 FPM/应用，保留 ACME 路径、文件及共享服务。扩展到期时间不会自行恢复，必须显式恢复访问。

面板运行配置仍在 `/etc/opsview/panel.json`，新增 `protocol`、`entry`、`allowedHost` 和可选 `allowedCIDRs`。缺少协议的旧配置保持 HTTPS；没有新 CIDR 字段时沿用旧元数据限制。新访问变更以 120 秒事务暂存，确认 token 仅返回一次且磁盘只保存哈希。执行器持有回退记录，面板重启后仍可回退。root 可用 `ov recover-access --all` 清除访问限制和待确认记录。

`ov ssl status|on|off|issue|renew` 管理面板协议与证书。续期注册信息加密保存于 `managed/panel-acme/renewal.enc`，每小时按剩余寿命检查，关闭 HTTPS 时暂停。上传证书会停止之前的自动续期。证书、密钥和该目录都应纳入离线灾难恢复规划。

从 v0.1.3-rc.2 开始，`ov` 菜单第 15 项直接执行关闭 SSL，确认后切换到 HTTP 并打印重新连接链接。必须在 120 秒内通过该链接确认，否则恢复原协议。关闭 SSL 保留证书。HTTPS 服务正常但客户端报证书不受信任时，可先检查自签名证书信任，或使用此入口恢复 HTTP 访问。

v0.1.3-rc.3 起，HTTPS 会话继续使用 Secure 的 `opsview_session`，HTTP 使用独立的 `opsview_session_http`，避免关闭 SSL 后旧 Cookie 阻止登录。HTTP 升级后需要重新登录一次，账号密码保留。旧版遇到账号验证成功但返回登录页时可用无痕窗口或清除此面板站点的 Cookie 临时恢复。

新菜单包含部分尚未交付的高级能力，不能按菜单名称推定已支持；具体范围见根目录 [软件管理实现说明](../软件管理实现说明.md)。

原生新配方按系统验收后才启用：`verifiedSystems` 分别登记 source/binary 的精确系统；未通过安装及服务验收的组合会在市场禁用并由执行器再次拒绝。仅有编译/协议 smoke 不能标记为完成。终端修改与网页确认/超时回退使用共同文件锁；已有待确认变更时终端协议、端口或地址修改会拒绝。

### Redis 与网站配置

备份资源可选择受管原生 Redis：获取一致性 RDB、通过对应版本 `redis-check-rdb` 校验后加密，进入现有本地/S3 备份历史和保留策略。恢复须选择另一个已安装、正在运行且没有关联业务的空 Redis 实例；先检查目标版本兼容性，保留原目录，以 RDB 模式加载，再按目标原策略重建 AOF。成功后人工验证并切换应用连接。单独的“后台快照”按钮仅请求 BGSAVE，不等同于完整备份。

PHP Session 登记 Redis 依赖，依赖未移除时不能直接卸载或修改 Redis 端口。MySQL 内存档位只填入候选表单，预算模型不包含临时表等额外开销，确认配置差异后才应用。

网站运行目录相对受管根目录设置，例如 `public`；禁止符号链接及目录跳转。默认文档和伪静态从受控模板生成。反向代理每条前缀规则可配置多个同协议上游，默认保留请求路径，必须保留 `/` 规则；上线前核对 Host、WebSocket 和超时配置。

## 软件日志筛选与归档

从软件市场进入实例设置的日志页，PHP 可选择站点范围、错误日志或 FPM 日志；systemd 日志使用该范围对应的服务。时间选择器采用浏览器时区，传输时携带时区；无时区日志使用服务器时区。级别精确匹配，无法识别时间的记录在时间筛选时排除。每次读取最多扫描末尾 1 MiB、默认返回 500 条记录，多行慢查询或堆栈作为整条记录保留。下载范围与当前筛选结果一致且经过脱敏。

点击“归档并清理”前先读取最新结果并确认影响。Nginx 可在线重开日志，执行器核对 master/worker 文件描述符以确认切换，失败尝试恢复；其他软件须先停止对应服务，PHP 站点只需停止选定站点的 FPM。当前日志完整保存在 `log-archives/<实例 ID>/<实例或站点 ID>/<类型>/`，新日志保留原属主和权限。操作不改变实例运行状态和业务配置。归档仍占用磁盘，可选择归档查看并单独确认删除；页面展示最近 100 个，没有自动轮转或保留清理。journal 由系统管理，MySQL binlog 清理继续受复制及备份依赖限制。

Nginx 重开失败的回退还会保留替换文件为独立私有归档，即使检查时为空也不删除其 inode，避免重开期间并发写入的日志丢失。

## PHP Session 连接与回退

在 PHP 设置中选择版本默认配置或指定网站。默认配置仅由新网站继承，已有网站不批量覆盖。文件模式使用该范围私有目录；Redis 关联受管原生实例；memcached 只接受 `127.0.0.1` 和有效 TCP 端口，不接受远程地址、URL 或自定义连接参数。memcached 服务需独立安装并启用二进制协议，对应 PHP 扩展通过固定配方安装。

memcached 可选 SASL，密码以遮盖占位符返回，提交占位符保留当前密码；关闭认证或切换到其他存储会清除当前 ini 中的 SASL 凭据。SASL 用户名、密码不能包含控制字符、双引号、反斜杠及 `${`，避免 INI 字符串或环境变量展开歧义。凭据保存在 0600 的 PHP 配置和加密元数据中，历史配置加密；PHP 会话检查进程通过标准输入及私有 FastCGI 请求传递预期值，SASL 凭据不写入参数、环境变量和任务日志。Redis 的连接预检沿用既有 `redis-cli` 的 `REDISCLI_AUTH` 环境认证方式，实际 PHP 会话检查使用私有通道。

应用流程为：核对修订号和服务状态 → 私有候选配置会话读写 → 再次检测外部修改 → 加密历史 → 原子替换及 FPM 重载 → 实际 FPM 有效参数和会话读写检查。检查脚本临时放在范围对应的受管 `run/`，位于网站公开目录之外，完成或失败后删除。重载后会等待新工作进程使用正确配置，检查失败恢复原文件、权限并重新重载和检查 FPM。回退失败会明确报错，需查看服务及加密配置历史。

每次显式保存 Redis/memcached Session 时使用 `ov.<资源 ID>.` 前缀，新网站继承时替换为该网站的独立前缀。旧网站不会因面板更新自动更改；显式切换或保存可能使旧会话失效。即使网站已暂停，仍在使用的会话扩展也禁止直接停用或卸载，必须先修改其存储方式。该依赖检查不替代操作系统管理员对外部 memcached 服务的维护。

## 服务器文件管理

文件页默认浏览 `/`。路径栏、面包屑、上级、最近访问、收藏和网站根目录链接用于定位；普通隐藏文件可通过“显示隐藏文件”查看，含 `.env`。列表提供排序、名称筛选、服务器分页、备注、权限/所有者及批量选择。

上传通过面板向执行器 Unix Socket 流式转发，8 MiB 分块、单文件 2 GiB。浏览器保存上传会话标识，暂停后重新选择同一目标、大小和修改时间的文件可续传；24 小时未使用的会话由每小时清理任务回收。下载经当前登录态转发，支持 Range，服务器文件不会成为公开静态地址。

UTF-8 编辑默认最多 10 MiB。保存前查看差异，后端比对修订号；冲突返回 409，先重新载入再合并。原子保存保留权限和所有者，旧文本副本进入回收站。新建文件 0644、目录 0755。复制、跨分区移动、压缩解压、递归搜索及大小计算进入任务中心，可以取消并查看结果。内容搜索只返回匹配文件路径，不把文件内容写入通用日志或审计。

同名默认跳过，可以选择保留两份或覆盖；覆盖保留回收副本，发布失败尝试恢复。跨分区移动先复制并核对文件内容/修订，发布后源文件进入原文件系统回收站。批量任务按项执行，取消不会撤销已经完成的项；检查任务结果及回收站后继续。

回收站目录在源文件系统内（与受管目录同文件系统时使用受管根下 `.opsview-trash`），目录 0700，中央元数据只由执行器读写。普通删除默认保留 30 天，每小时检查过期项；回收站不可用时拒绝删除。恢复默认原路径，冲突须选择新的完整路径；永久删除及清空必须单独确认。

ZIP/TAR.GZ 归档限制为 10000 项、总展开量 8 GiB，拒绝绝对/越界路径、链接和特殊文件，暂存校验后逐项发布；不默默合并同名目录。递归任务最多 100000 项，名称/内容搜索最多 1000 个结果，内容扫描最多 256 MiB。递归默认不跟随符号链接或跨挂载点。磁盘空间预检保留额外 16 MiB，不预留业务未来增长空间。

`/proc`、`/sys`、`/dev`、`/run` 仅显示基础信息。面板/执行器主密钥、备份密钥、认证数据库、状态、执行器 Socket、信任目录、回收站和上传元数据受保护；普通硬链接文件只展示属性。关键系统目录不允许整目录删除、移动或递归改权。执行器使用目录句柄、实际描述符路径和 inode 检查，包括受保护文件的挂载别名。面板继续使用普通用户；执行器的 ProtectHome/PrivateTmp 关闭以操作真实服务器目录。

公开分享、同步、企业防篡改和网页终端未提供。外部程序持续写入时，复制不等于应用一致性备份。

## v0.1.3 兼容迁移与手动回退

升级将 SQLite schema 从 1 升至 2，保存准确版本占用，迁移旧应用网站的上游并取消排队的应用变更；保留原账号、业务文件及 systemd 进程。卸载保留数据记录仍占用版本，在实例列表“清理记录”后才可重新安装。旧容器实例仍受管理，新增容器模板位于 Docker。

更新失败时脚本自动恢复更新前面板目录、二进制、服务单元、运行配置和 ov 入口。更新成功后需要降级时，使用本次更新打印的**升级前**完整快照；旧程序会拒绝 schema 2，不能仅覆盖二进制。手动回退前停止两个服务，把当前面板目录另存，再恢复快照的 `panel/`、`bin/`、`units/`、`panel.json` 和 `ov`，执行 daemon-reload 并启动两个服务。用户的 drop-in 保留。业务软件与业务数据不由此回滚；升级后新做的面板资源变更需要人工核对。

测试版更新命令：

```bash
ov backup
ov update --version v0.1.3-rc.5
```

本版本不触发用户服务器自动更新，正式 latest 入口独立管理。

### 极速软件包发布与核对

首批六个推荐版本在四系统共 24 个 Linux amd64 组合已通过构建及独立干净系统复验，目录固定引用 `software-44057101979`。原始报告保存在 `docs/releases/v0.1.3-rc.1-native-acceptance.json`。发布会核对报告、systemd 和协议结果，不覆盖既有软件标签或附件。

GitHub 的集成令牌可能拒绝为修改工作流的提交创建发布标签。维护者须先通过 Git 推送与验收提交一致的 `software-<run-id>` 标签；当前 `software-publish-<run-id>` 流程按声明矩阵和各任务最新结果复用全部已验收附件，再次校验原始提交、标签、报告和摘要；历史 24 组合流程保留在当时的软件标签中。整个过程不需要目标服务器重新编译。


## 紧凑界面与数据刷新（v0.1.3-rc.4）

管理页面取消顶部大标题、宣传区和全局“刷新状态”。只有概览显示顶部右侧信息；其他页面可从侧栏底部账号菜单退出，窄屏从页面工具栏的菜单按钮打开导航。文件、日志与失败提示保留局部刷新或重试入口。

菜单切换复用会话内列表缓存和标签、筛选、分页状态；当前页数据按设置的周期刷新，软件目录缓存五分钟，软件/仓库变更后失效。后台标签页暂停轮询，重新激活时刷新；退出登录清空缓存。文件正文、密码及配置密钥不存入导航缓存。首次下载代码及服务器实际操作仍受网络和机器负载影响。

文件列表、搜索、收藏和最近访问隐藏禁止访问项目及其链接，包括面板认证数据、密钥、内部临时数据和 `/proc`、`/sys`、`/dev`、`/run`。`/etc`、`/usr`、`/var` 等目录仍可浏览；整目录危险操作限制保持。开启“显示隐藏文件”可查看普通 `.env`，不会显示受保护项目。直接输入受保护路径也会拒绝，不删除实际文件。

测试版更新：`ov update --version v0.1.3-rc.5`。自动更新失败会按安装器流程回退；需要主动退回上一版时执行 `ov update --version v0.1.3-rc.3`。本版没有数据库迁移，不需重置账号或业务数据。

## rc.5 数据库迁移与回退

rc.5 增量迁移到 SQLite schema 3，新增数据库账号绑定及回收记录，库名唯一性调整为“同实例内唯一”。数据库资源、管理员、备份与关联网站的原 ID 保留。数据库新页面操作见 [数据库管理说明](database-management.md)。

升级前执行 `ov backup`，再执行 `ov update --version v0.1.3-rc.5`。更新器停服后自动复制完整面板目录（包括 SQLite、WAL 和 master.key）、旧二进制/更新脚本、systemd 单元、运行配置与 ov 入口至终端打印的 `/var/lib/opsview/updates/<时间-进程号>/`。更新失败自动恢复这份快照。

成功升级后回退 rc.4，**不能只执行旧版本更新命令**，旧程序会拒绝 schema 3。选择本次更新之前的快照，在没有后台变更任务时以 root 执行以下步骤；先将占位路径替换为已核对的真实目录：

```bash
OPSVIEW_ROLLBACK=/var/lib/opsview/updates/替换为本次升级前快照目录
# 下面检查必须全部成功，确认快照来自 rc.4，并包含原主密钥。
test -f "$OPSVIEW_ROLLBACK/panel/opsview.db" && test -f "$OPSVIEW_ROLLBACK/panel/master.key" && test -x "$OPSVIEW_ROLLBACK/bin/opsview" || exit 1
systemctl stop opsview-panel.service opsview-executor.service
OPSVIEW_SAVED=/var/lib/opsview/updates/before-manual-rollback-$(date -u +%Y%m%dT%H%M%SZ)
install -d -m 0700 "$OPSVIEW_SAVED"
mv /var/lib/opsview/panel "$OPSVIEW_SAVED/panel"
mv /opt/opsview/bin "$OPSVIEW_SAVED/bin"
cp -a "$OPSVIEW_ROLLBACK/panel" /var/lib/opsview/panel
cp -a "$OPSVIEW_ROLLBACK/bin" /opt/opsview/bin
cp -a "$OPSVIEW_ROLLBACK/units/opsview-panel.service" "$OPSVIEW_ROLLBACK/units/opsview-executor.service" /etc/systemd/system/
cp -a "$OPSVIEW_ROLLBACK/panel.json" /etc/opsview/panel.json
ln -sfn /opt/opsview/bin/opsview /usr/local/bin/ov
systemctl daemon-reload
systemctl reset-failed opsview-panel.service opsview-executor.service
systemctl start opsview-executor.service opsview-panel.service
ov status
ov info
```

保留 systemd drop-in。快照含密钥，禁止放入网站目录或公开分享。此操作恢复面板元数据，不撤销 rc.5 期间已执行的建库、改密、删库或 Redis 写入；若升级后已操作业务，先单独备份业务并核对执行器与元数据关联，再决定回退。SQL 回收备份在执行器备份目录中加密保存，需保留对应 `backup.key`，面板元数据备份不能替代业务备份。

## rc.6 网站目录与进程托管

rc.6 新项目默认使用 `/www/wwwroot/域名`，旧项目不搬迁；备份和文件入口跟随执行器登记的实际目录。`/www`、`/www/wwwroot` 由 root 所有且不允许组或其他用户写入；新建的 PHP/托管项目目录由非 root 运行用户所有，不递归更改已纳管文件。

更新前执行 `ov backup`，更新命令为 `ov update --version v0.1.3-rc.6`。schema 仍为 3，无需额外数据库迁移。更新失败沿用安装器自动回退。

已成功更新后的人工回退：先停止新部署的托管网站并备份项目文件、运行配置和完整面板数据；记录本次新增的站点和专属 systemd 服务。不要只替换程序后继续操作 `managed` 类型资源。按上一节的停服、快照恢复步骤，恢复升级前完整 panel 目录、bin、运行配置和单元。新建业务服务和 Nginx 配置不会因面板快照回退自动撤销，需在停用并备份后核对清理，保留项目目录。旧外部应用不纳入新服务清理范围。回退后检查两个面板服务、实际网站和登录。

网站内进程托管及基础扫描的范围与限制见 [网站管理](website-management.md)。

## rc.7 软件市场与版本选择

携带 `?software=php` 等筛选参数打开软件市场时，顶部显示当前筛选及“显示全部软件”按钮。核心软件的安装状态以当前所选准确版本为准；已安装版本隐藏重复安装入口，选择其他版本可继续安装。安装失败和卸载保留的数据记录仍占用版本，先查看任务现场，再按既有卸载及清理记录流程操作。

旧 PHP 使用实例内的隔离构建依赖，极速包包含重定位所需的兼容文件和构建辅助元数据。MySQL 客户端 ABI 兼容库仅作用于该实例。各安装组合必须通过真实 systemd 服务与协议探测、全新系统安装验收后，才能写入 `verifiedSystems`；目录可见不等于已开放安装。不会因失败自动切换安装方式。

更新面板使用 `ov backup`、`ov update --version v0.1.3-rc.7`，业务软件仍由管理员选择准确版本安装，既有业务软件不自动升级。版本支持以可信目录与验收记录为准。
