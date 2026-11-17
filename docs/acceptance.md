# 验收记录和发布门槛

版本：0.1.1 开发测试版。记录日期：2026-11-17。

## 已执行的验证

- macOS 开发：Go 单元/集成测试及 race、go vet；覆盖认证、CSRF、来源校验、加密元数据、任务锁与重启、日志脱敏、TOTP、签名与篡改、路径与归档越界、备份块认证与截断、Compose 权限、面板备份恢复。
- 前端：TypeScript 类型检查、Vite 生产构建、Vitest API 测试。
- Playwright：真实 GoFrame HTTP 服务，所有菜单、非法配置拒绝、缺失执行器的失败任务、桌面与移动布局，以及面板备份与 SSE 下载流程。
- 隔离 Docker 容器：Nginx 静态站及错误配置保留旧进程；MySQL 专用账号、逻辑导出及新库恢复；PostgreSQL 专用账号、角色导出、custom dump 及新库恢复。
- Linux 容器：Ubuntu 22.04/24.04、Debian 12/13 的跨编译 Go 测试。容器测试不等于真实服务器安装验收。
- 本地构建与 Linux amd64 发布包。发布包包含嵌入前端的面板、执行器、worker、签名工具、安装及更新脚本。
- GitHub Actions Ubuntu 24.04 amd64 真实 systemd：首次安装、创建管理员、目录 owner、重复安装拒绝、正常更新，以及故意替换错误二进制后的服务/管理员/证书回退；由 `scripts/test-deployment.sh` 验证。发布工作流在这些检查通过后上传 Release。[v0.1.0 发布运行](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/43815879782)。这不包含服务器重启、公网网络或其他发行版的完整部署验收。
- 安装器在 Ubuntu 22.04/24.04、Debian 12/13 隔离容器中验证固定标签下载、失败阻断、摘要及归档检查。公开 `releases/latest/download/install.sh` 已下载并与源码及 SHA256SUMS 对照，公开发布包的 SHA-256 和归档检查通过。

测试脚本与断言为证据来源；新增功能应随改动再次执行相关检查。容器 smoke 验证底层软件行为，尚未贯穿浏览器到特权执行器的完整部署流程。

## v0.1.1 安装引导与维护验证

- 本地通过：Go race / vet、账号事务及回滚、随机凭据与会话失效、TOTP 保留、菜单编号和 EOF、无交互帮助、受控更新参数、运行配置和公网返回值校验。
- 端口测试使用受控命令夹具验证占用检查、UFW 来源限制、已存在规则识别、保留旧规则、未知防火墙诊断和本机健康失败回退。
- 面板备份验证运行配置快照的加密与新目录恢复；Playwright 5 个流程通过。Ubuntu 22.04/24.04、Debian 12/13 容器测试通过。
- GitHub Ubuntu 24.04 的真实安装回归已通过：[v0.1.1 安装回归运行](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/43833496942)。测试由 `scripts/test-deployment.py` 执行，覆盖随机账号直接登录、凭据不进入日志、root 维护、隐藏密码输入、端口变更后更新、服务失败回退，以及 v0.1.0 已初始化/未初始化两种迁移。凭据只在测试进程内存中捕获。
- 安装及更新验收要求面板和执行器运行、执行器 Socket 就绪，且连续三次本机健康检查通过；故障注入验证失败后回退。发布工作流额外从公开 latest 安装入口验证八个阶段、随机账号、改端口后通过受控脚本执行真实网络更新。
- 真实公网 IPv4/IPv6 入站、云安全组和 UFW 外部来源验证仍需独立机器验收；受控命令测试和本机健康检查不替代这些验证。

## 发布前仍需完成

| 项目 | 待验证内容 |
| --- | --- |
| 四种 Linux 系统 | 在干净虚拟机执行安装、原生编译、MySQL 官方二进制依赖、systemd 启停、重启恢复和安装中断 |
| 网络 | 独立机器的 IPv4/IPv6、DOCKER-USER 白名单、规则顺序、超时撤回、执行器重启、SSH 自锁保护、云安全组 |
| HTTPS | 真实公网 ACME 首次申请、自动续期、443 被占用、证书失败回退与错误域名 |
| S3 | 指定供应商的凭据和 Endpoint，上传、校验、保留、断网失败、远端恢复、低权限 worker 沙箱 |
| 升级与恢复 | 真实已安装版本的升级失败回退、业务数据/执行器密钥灾难恢复演练、数据库迁移和应用切换 |
| 插件 | 真实 systemd worker，第三方依赖缺失、资源隔离、升级失败回退、撤销公钥后卸载 |
| 浏览器到服务器 | 全新 Linux 从登录、选版本、安装、建站、证书、数据库、Docker、备份到恢复的端到端流程 |

## 当前明确限制

- 单管理员、单机；旧 PHP 隔离依赖、集群、团队权限、完整 SQL 工作台、源码在线构建、DNS-01 提供者未实现。
- 已有服务只诊断，不接管；迁移通过受管理新实例的逻辑导入或迁移完成。
- 自定义源只允许现有 Nginx/MySQL/PostgreSQL/Node/PHP/Redis 安装器及签名清单，不执行自由 Shell 步骤。
- 插件清单继续兼容 minPanelVersion 0.1.0（面板 0.1.1），权限限 docker.compose、backup.s3、network.outbound、metrics.read；不是通用 root 插件运行时。S3 备份适配器在执行器内，外部 worker 提供受限检查扩展。
- 网页文件上传和读取 10 MiB、ZIP 解压总量 512 MiB、SQL 恢复 256 MiB、迁移验证 1000 张表；大规模业务需扩展流式能力和验收。
- PostgreSQL 自动迁移目前只验证 public schema；MySQL 自动迁移只开放 8.4 LTS 内路径。行数验证不包含业务语义。
- 面板任务全局单 worker 串行，优先保证操作可追踪；安装任务支持进程组取消，并行吞吐仍待优化。
- 面板概览展示宿主机指标；Docker 资源快照显示 Engine 原始统计，尚未实现长期监控存储和告警通知渠道。
- GitHub Releases 提供终端一键安装和显式 `--update`；没有网页自动更新。业务软件不会自动更新。

通过表格中的真实 Linux 验收前，不将此版本标记为完整生产首版。

## 软件设置增量验证（2026-11-17）

- 8 个 Playwright 流程通过：管理菜单、网站注入拒绝、执行器失败显示、移动布局、加密备份/SSE、三类网站表头及 Nginx 前置条件、软件配置冲突、面板协议暂存。截图保存在本地 `.opsview/screenshots/`。
- 安装器 16 项检查通过；10 个 Go 包测试在 Ubuntu 22.04/24.04、Debian 12/13 容器执行通过，其中覆盖配置修订/加密草稿、符号链接拒绝、失败文件回退、流量轮转/截断、过期访问策略、面板确认 token 和超时回退。
- Nginx 1.30.6：临时 Ubuntu 24.04 真实下载、摘要、低权限源码编译、进程启动、loopback 探针通过，生成原生构建附件。该测试使用模拟 systemctl，不能替代完整 systemd 安装验收。
- Redis 8.10.3 在修正 LTO 和可选模块构建入口后，Ubuntu 24.04 的真实源码编译、启动和协议验证通过，生成归档及摘要。PHP 8.4.27 首轮本地暂存安装失败（INSTALL_ROOT 与 DESTDIR 差异）已修复；GitHub 四系统构建、常用内置扩展及真实 FPM/systemd 验证均通过。本地 amd64 模拟编译已由 GitHub 原生验证替代并停止。
- 新增 `candidate.yml`、`native-packages.yml`，已推送开发分支并执行 GitHub 候选构建；未发布正式版本。完整未完成清单见 [软件管理实现说明](../软件管理实现说明.md)。

### GitHub 第一轮证据

提交 `6eda74a`：

- [面板候选构建](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/43915306490)：通过，含 Go race/vet、前端、8 个 Playwright、安装器、四系统包测试、发布包、真实部署回归。
- [原生软件验证](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/43915306499)：12 个源码构建/协议组合和 3 个 Ubuntu 24.04 真实 systemd 服务全部通过。
- [常规检查及部署](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/43915306008)：通过。

仅提升三个推荐版本在 Ubuntu 24.04 的源码安装；其他方法及系统仍遵守受信任目录的验收门槛。后续 Redis 备份、网站选项和 CLI 远端确认改动另行回归，最终结果在本节追加。

本地后续增量：9 个 Playwright 通过（新增多路径代理编辑/提交），四系统 40 个包测试通过，安装器检查、前端构建和 Vitest 通过。新增 Redis 保存策略合并、恢复目录保护、MySQL 内存估算、网站运行目录/代理注入拒绝与 CLI 确认事务测试。

### 最终功能提交验证

功能提交：`1b8816f7b83fd4823cbbcb31d929efd38daf1f1e`。最后的文档整理不改变功能代码。

| 工作流 | 结果及范围 |
| --- | --- |
| [常规检查及部署](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/43924340436) | 通过；Go race/vet、前端类型/构建/Vitest、9 个 Playwright、Docker 冒烟、四系统包测试、面板真实 systemd 安装与更新回退 |
| [候选构建](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/43924340487) | 通过；重复执行发布包门槛及面板真实部署，上传候选附件，未创建 Release |
| [原生软件验证](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/43924340428) | 全部通过；Nginx 1.30.6、PHP 8.4.27、Redis 8.10.3 × 四系统，共 12 个源码构建/协议组合，以及三个 Ubuntu 24.04 真实 systemd 服务 |

真实 systemd 新增检查已通过：

- Nginx：实际运行目录、默认文档、404 模板、隐藏文件禁止访问、多路径反向代理及 Host。
- Redis：加密备份、RDB 校验、空目标实例恢复、多个逻辑库、AOF 重启持久化；拒绝覆盖非空目标。
- PHP：两个独立站点 FPM 及私有 Socket、站点普通用户写入独立 Session/错误日志、Session 目录 0700、版本目录无站点会话文件。
- 面板：`ov port`、`ov ssl off/on` 与网页共用新监听地址确认，待确认时拒绝更新；真实 HTTP/HTTPS 确认成功后更新可继续。

容器数据库就绪探测改为 TCP，避免将镜像初始化阶段的临时 Socket 服务误认为正式服务；失败诊断脱敏后进入 CI 注释。修正后的容器检查已通过。

候选附件名：`opsview-candidate-1b8816f7b83fd4823cbbcb31d929efd38daf1f1e`，包含发布包、SHA256SUMS 和 bootstrap。候选试装使用包内 `deploy/install.sh` / `deploy/update.sh`，见根目录 [安装教程](../安装教程.md#候选包试装)。

## 2026-11-17 软件日志续作

新增日志时间、精确级别及站点范围筛选、PHP FPM/systemd 日志切换、多行记录、自动刷新、当前脱敏结果下载和归档查看/删除。归档需要确认和最新修订；Nginx 在线 reopen 验证 master/worker 描述符释放旧 inode，失败恢复；其他软件需停止对应服务。

本地已通过：Go 全量 race 测试及 vet、前端类型检查/构建与 Vitest、10 个 Playwright 用例、16 项安装器检查、四系统各 10 个 Go 测试包。新增用例覆盖时区（内嵌 zoneinfo）、查询窗口与参数校验、多行慢查询、先脱敏再搜索、符号/硬链接及归档路径拒绝、站点隔离、归档权限、服务运行保护、重开失败恢复和维护任务不改变实例状态。

`./scripts/test-log-rotation.sh` 使用目录中固定摘要的 Nginx 镜像，在无外部网络的临时 Linux 容器中连续执行两次在线轮转，验证实际进程描述符、新日志继续写入及原日志留存，已通过。该验证不等于给新的原生安装版本或系统组合放行。真实 Ubuntu 24.04 原生 Nginx systemd 验证加入同一流程，GitHub 结果见下节。没有更新生产服务器或发布正式版本。

### 日志功能 GitHub 验证

功能提交 `c54731c1ea1afa16d6463f41adf7aa06afebbe97`（包含重开失败时保留替换 inode 的修复）：

| 工作流 | 结果 |
| --- | --- |
| [常规检查及部署](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44006641163) | 通过，含 Go race/vet、前端、10 个 Playwright、Docker、四系统包、真实 Nginx 轮转及面板部署回退 |
| [候选构建](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44006641181) | 通过；候选附件 `opsview-candidate-c54731c1ea1afa16d6463f41adf7aa06afebbe97`，没有创建 Release |
| [原生软件验证](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44006641198) | 全部通过，包含 Ubuntu 24.04 原生 Nginx 的实际 systemd 在线轮转 |

以上为日志功能提交的验证，后续 PHP Session 代码独立记录，不能借用这些结果宣称通过。

## 2026-11-17 PHP Session 续作

新增已有本机 memcached 连接及可选 SASL、密码占位符保留、站点独立前缀、扩展依赖保护。Session 应用增加候选真实读写、预检后外部修改检测、FPM 有效配置及读写检查；配置或服务失败恢复原文件、权限并检查服务。软件设置左右两栏限制在视口内并独立滚动。

本地通过 Go 全量 race/vet、前端类型检查和构建、Vitest、11 个 Playwright（新增 Session 范围/密码遮盖/取消/提交及弹窗边界）、16 项安装器检查。Go 回归覆盖无效端口和 INI 注入拒绝、凭据只经私有通道、候选/重载/FPM 验证失败回退、外部漂移、停止网站的扩展依赖保护及检查文件清理。

Ubuntu 22.04/24.04、Debian 12/13 各 10 个 Go 测试包全部通过。旧 Redis 配置仍显示其实际前缀，不会在未保存时显示为已经隔离；更新面板不会自动更改旧网站的会话前缀。

后续复核补充：未知 Session 处理器不返回不透明连接字符串；站点 Session 及文件清理任务只持久化明确关联的范围，保留版本实例的运行状态、配置修订和业务元数据。新增对应任务队列回归，执行器及逻辑层 race 测试通过。

`./scripts/test-php-session.sh` 在临时 Ubuntu 24.04 容器使用发行版 PHP 8.3/FPM、memcached 扩展及真实 memcached 验证：无认证 Session、实际 FPM 生效检查、停服失败保持配置、SASL、错误密码拒绝、遮盖密码保留和两个站点命名空间隔离，已通过。此协议测试不放行任何新的目录版本或系统组合；推荐 PHP 8.4.27 的固定扩展配方和真实 systemd 验证另由 GitHub 原生工作流执行，也已通过，见下节。

### PHP Session GitHub 验证

最终功能提交 `53def0fddba6ee3b0aae60db97eacabbee9bac1a`：

| 工作流 | 结果及范围 |
| --- | --- |
| [常规检查及部署](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44011530475) | 通过；Go race/vet、前端、11 个 Playwright、Docker、四系统包、Nginx 轮转、真实 PHP/FPM/memcached 协议及面板 systemd 更新回退 |
| [候选构建](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44011530504) | 通过；发布包及面板真实部署门槛通过，已上传候选附件 |
| [原生软件验证](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44011530626) | 16/16 项全部通过；三个推荐版本 × 四系统的 12 个源码构建/协议组合、三个 Ubuntu 24.04 真实 systemd 服务及矩阵生成 |

候选附件为 `opsview-candidate-53def0fddba6ee3b0aae60db97eacabbee9bac1a`。未创建正式 Release，也未更新当前服务器。原生软件验收不会自动扩大目录中的安装放行范围。

本次 PHP 8.4.27 的 Ubuntu 24.04 真实 systemd 测试还通过固定 PECL memcached 3.3.0 源码配方安装，并验证 Session、SASL、错误密码拒绝、占位符保留、站点前缀隔离及 FPM 有效配置。这个扩展验证不等于全部 PHP 版本和扩展矩阵完成。后续文档提交不改变功能代码。

## 2026-11-17 v0.1.2-rc.1 公开测试版

发布提交 `1d382217a7830a4c37d6d89e9582d38a11ecfe56`，标签 `v0.1.2-rc.1`。用户授权发布测试后已创建 [GitHub 预发布版本](https://github.com/gzdzh-cn/dzh-OpsView/releases/tag/v0.1.2-rc.1)，未更新现有服务器。`latest` 经公开 API 核对仍为 `v0.1.1`；测试版需显式指定版本。

| 工作流 | 结果及范围 |
| --- | --- |
| [发布与公开安装](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44014583118) | build、publish、verify-public-install 全部通过；版本一致性、Go race/vet、前端与浏览器、安装器、真实 Nginx 轮转、PHP Session 协议、发布包校验及 systemd 安装更新回退 |
| [常规检查](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44014548906) | 通过 |
| [候选构建](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44014548881) | 通过 |

公开安装验证在全新 Ubuntu 24.04 GitHub 临时主机从固定标签下载脚本并校验，完成八个安装阶段、随机账号登录、ov 入口、端口修改及重新连接确认、改端口后的在线更新、再次登录和日志无明文密码检查。此次安装验证没有使用生产服务器。

本机另外直接下载三个公开附件，验证 `install.sh` 和发布包 SHA-256、归档路径及类型、四个二进制和版本文档。发布包大小为 23,620,253 字节，SHA-256 为 `b530bb3891e38c056a51d3e7cea184df7c54cd8453f0467b7c78ce38365f154a`。已公开标签及附件保持原样，后续文档不重打发布包。

原生配方的有效验收范围仍以上文最终功能提交的 16 项结果为准；发布提交触发的额外原生矩阵尚在排队，未将其记为通过，也未扩大安装放行范围。本版为预发布测试，前述完整版本矩阵、跨服务联动及生产验收待办继续有效。


## 2026-11-17 rc.1 菜单故障与 rc.2 修复

用户更新 rc.1 后报告左侧菜单无响应。在实际 Edge 中点击复现，控制台显示 `_plugin-vue_export-helper-BDNMzG2s.js` 返回 404，随后多个页面动态导入失败；服务器本机请求确认同一文件为 404，服务健康接口正常。根因是 `//go:embed dist` 默认排除下划线开头的文件，前端构建输出存在但未进入面板二进制。

rc.1 的浏览器测试使用 `--web ../web/dist`，所以验证的是磁盘文件，不能证明内嵌发布资源完整；原先菜单测试主要通过直接访问 URL，也没有覆盖全部实际菜单点击。rc.1 的流水线通过记录保留，已补充此验证缺口，不应继续推荐 rc.1。

rc.2 改为 `//go:embed all:dist`，加入构建目录与内嵌目录逐文件比较。该回归在修复前准确报出缺失 Vue helper，修复后通过。Playwright 默认移除 `--web`，逐个点击全部菜单并检查资源失败；发布和候选流水线增加 `OPSVIEW_E2E_RELEASE=true`，直接启动打包后的 Linux 二进制重复完整浏览器验证。其他软件及生产验收边界继续保留。
