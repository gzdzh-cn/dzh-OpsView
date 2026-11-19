# 验收记录和发布门槛

当前目标版本：v0.1.3-rc.18；下方保留历史版本的实际验收记录。记录日期：2026-11-19。

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
- 服务器文件上传 2 GiB、文本编辑 10 MiB、ZIP/TAR.GZ 10000 项/8 GiB、SQL 恢复 256 MiB、迁移验证 1000 张表；大规模业务需扩展流式能力和验收。
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

修复提交 `4355d80f760379a452694a5f3d298fbf336132a7` 已发布为 [v0.1.2-rc.2](https://github.com/gzdzh-cn/dzh-OpsView/releases/tag/v0.1.2-rc.2)，保持 prerelease，latest 仍为 v0.1.1。以下检查全部通过：

| 工作流 | 结果 |
| --- | --- |
| [发布与公开安装](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44016838633) | build、publish、verify-public-install 全部成功；内嵌文件完整性、Go race/vet、前端、11 个浏览器测试、实际 Linux 发布二进制的另一次 11 个浏览器测试、安装器、日志/Session 协议、systemd 安装更新回退及公开下载安装更新 |
| [常规检查](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44016811589) | 通过 |
| [候选构建](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44016811636) | 通过，包括实际打包二进制的浏览器回归 |

GitHub 公开附件元数据中，发布包大小为 23,625,366 字节，SHA-256 为 `b7f451db6eba9dd47f041b57f121613dd0dab692e7d0c24c0f6c1f0302766cca`；工作流内下载及校验通过。本机额外直连 GitHub Release 下载暂时超时，未记为本机独立下载验证通过。原生软件矩阵另行运行，未借此修复扩大安装放行范围。

此次仅通过 SSH 读取用户服务器状态和在 Edge 中复现，没有自动更新现有服务器。已有面板使用 `ov update --version v0.1.2-rc.2`，完成后重新加载页面。rc.1 已公开附件保留，不覆盖已有标签。

## v0.1.3-rc.1 本轮验证

本地新增 Go 测试覆盖 Docker 1.41–1.47 最高交集、30 秒缓存与只读重探测、变更不重放；准确版本并发占用、历史重复记录释放、旧应用网站迁移和任务取消审计；文件修订及元信息、密钥软/硬链接拒绝、回收恢复冲突、发布失败回退、压缩包越界和特殊文件、Unix Socket 分块传输/中断/续传与 Range。

浏览器新增 Docker 引擎前置状态、应用入口下线/列表、服务器文件路径/编辑冲突/窄屏。保留真实内嵌资源与实际 Linux 发布二进制的全部侧栏点击。Linux 专项检查覆盖跨文件系统移动及受保护数据的 bind mount 别名，入口为 `scripts/test-files-linux.sh`，只允许临时 GitHub 主机。

原生包工作流拆为 24 个构建及 24 个干净系统复验，用真实 systemd 和协议验证；Node 也运行临时 HTTP systemd 服务。报告全部通过后发布不可覆盖的独立软件 Release，再提升目录。未通过的系统组合保持不可安装，CI 排队或源码编译成功不能替代极速安装验收。最终工作流结果在发布完成后追加。

### 极速包 24 组合验收与提升

软件配方提交 `8d7a44d84925db2896db8a3953b8a03c5c56e6bc` 的 [原生矩阵](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44057101979) 已完成 24/24 个构建和 24/24 个独立干净系统复验。范围为六个推荐准确版本 × Ubuntu 22.04/24.04、Debian 12/13，均验证真实 systemd 和协议；预编译复验更换安装目录，目标机不编译主软件。

原工作流的上传步骤因 GitHub 集成令牌创建发布时返回 403 而失败；已预先推送与验收提交一致的不可变软件标签，通过 [产物发布工作流](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44062049151) 重新验证原始任务和报告后发布成功，没有重建或替换报告。独立软件 Release 为 [software-44057101979](https://github.com/gzdzh-cn/dzh-OpsView/releases/tag/software-44057101979)，包含 16 个自建软件包及目录/报告，MySQL、Node.js 引用官方包。公开附件摘要与目录逐项核对，24 个组合已提升到两个目录副本；[原始报告](releases/v0.1.3-rc.1-native-acceptance.json) 随仓库保存。

日志页历史浏览器超时已从完整调用记录定位为时间范围弹层拦截点击；测试改为显式提交时间并断言弹层关闭。读取期间禁用下载和归档，等待当前响应后才操作。最终面板和公开安装发布结果另行追加，不能用软件包验收替代。

### v0.1.3-rc.1 公开发布结果

发布提交 `dad4f12`，标签 `v0.1.3-rc.1`，已公开为 [GitHub 预发布测试版](https://github.com/gzdzh-cn/dzh-OpsView/releases/tag/v0.1.3-rc.1)。以下最终检查全部通过：

| 工作流 | 结果及范围 |
| --- | --- |
| [常规检查及部署](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44063442564) | Go race/vet、前端类型/构建/Vitest、16 项安装器检查、Linux 文件传输/跨文件系统/bind mount、日志页连续 5 次和全部 14 个浏览器流程、Docker、四系统测试、真实日志/Session 协议、systemd 安装更新回退 |
| [候选构建](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44063442534) | 通过；实际打包 Linux 二进制再跑全部 14 个浏览器流程，包括逐个点击侧栏及内嵌静态资源检查，候选附件已生成 |
| [发布与公开安装](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44064471974) | build、publish、verify-public-install 全部通过；公开下载及校验、首次随机账号登录、ov、端口修改/重新连接、改端口后在线更新、再次登录及日志无明文密码 |

发布包为 23,754,246 字节，SHA-256 为 `9c802fb1217493c01d432b0a09d957d033ee7d44f830f0272103516b4729dee6`。本机额外下载 bootstrap 与 SHA256SUMS 并校验，通过 GitHub 公开附件摘要核对包摘要；完整包的公开下载和安装由上述 CI 完成，本机未额外完整下载。正式 latest 经公开 API 核对仍为 `v0.1.1`。

本轮没有更新用户服务器。使用 `ov backup` 后执行 `ov update --version v0.1.3-rc.1` 可选择测试。成功升级后降级需要升级前完整快照，不能只替换二进制；详细步骤见运维手册。后续验收文档追加不修改已公开标签和附件，完整首版及公网验收边界仍保留。

## 2026-11-17 v0.1.3-rc.2 SSL 菜单与访问恢复

用户手动更新后报告菜单 15 只显示状态、面板无法打开。SSH 检查确认两个服务运行正常，本机及公网 HTTPS 都返回 200；服务日志记录客户端拒绝自签名证书。通过现有 `ov ssl off --yes` 暂存 HTTP 配置，再从公网新监听地址提交确认，待确认文件已移除，服务健康正常，公网浏览器可加载完整登录页。没有修改管理员或业务数据，也没有自动更新用户服务器至新版本。

公网检查同时发现约 1 MB 的界面脚本未压缩，慢速连接存在长时间等待。rc.2 为静态 JS/CSS 等添加 gzip 协商压缩和带构建指纹资源的浏览器缓存；HTML 与接口保持 no-store。回归覆盖压缩还原、拒绝 gzip 的客户端、HEAD 及 HTML 缓存策略。

发布提交 `cafab1a`，标签 `v0.1.3-rc.2`。本地 Go race 测试、go vet、前端类型检查与构建、Vitest、14 项 Playwright 流程均通过。[发布流水线 44092122759](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44092122759) 的 build、publish、verify-public-install 全部成功，覆盖：

- 实际发布二进制的全部侧栏导航与静态资源加载。
- 真实终端选择菜单 15、取消不改配置、确认切换 HTTP、新地址确认、HTTP 登录、证书文件保留。
- Linux systemd 安装、访问配置事务、失败回退，以及公开发布包安装和通过 `ov update` 更新后保留 HTTP 与自定义端口。

已发布为预发布测试版，保留 rc.1 的标签及附件。用户可运行 `ov backup`、`ov update --version v0.1.3-rc.2` 获取菜单修复；现有证书文件无需删除。

## 2026-11-17 v0.1.3-rc.3 关闭 SSL 后的登录循环

用户更新 rc.2 后，点击登录返回登录页。只读检查确认服务运行、HTTP 配置保留，审计连续记录 `auth.login` 成功且会话已创建。因此没有重置密码或改写管理员。通过 Chromium 在非 localhost 地址保留旧 Secure Cookie，复现 HTTP 登录响应 200，但同名的新 Cookie 被浏览器拒绝写入。

根因是 HTTPS/HTTP 共用 `opsview_session`。rc.2 的 Linux HTTP 登录测试使用新 Cookie Jar，本地浏览器主要使用 localhost，未覆盖普通公网地址中遗留 Secure Cookie 的行为。

rc.3 将 HTTP 会话改为 `opsview_session_http`，HTTPS 保留原名及 Secure 保护；读取会话、接口鉴权、退出统一按协议选择。登录页在收到成功响应后再次检查会话，Cookie 被禁用或拦截时显示明确错误。本地完整 Go race、go vet、前端构建和 Vitest 通过，16 项浏览器测试全部通过，包括：

- 使用映射到本机的非 localhost 域名，保留旧 Secure Cookie，真实登录、刷新及侧栏导航成功。
- 登录成功但浏览器无法保存会话时明确报错，避免静默返回。
- HTTP/HTTPS Cookie 名称与属性、接口鉴权、双 Cookie 并存及退出清理对应会话。

旧 HTTP 会话在更新后需要重新登录；管理员、密码、业务数据与当前访问协议保留。更新前可使用无痕窗口或清除此面板站点的 Cookie 临时登录。

发布提交 `d356dfc`，标签 `v0.1.3-rc.3`。[发布流水线 44106709383](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44106709383) 的 build、publish、verify-public-install 全部成功；实际发布二进制的 16 项浏览器流程、systemd 安装与失败回退，以及公开下载安装、HTTP/自定义端口下 `ov update` 后登录均通过。已独立下载公开安装脚本并核对 SHA-256，发布包摘要为 `b3732ae215b4f6530625e7ac64008ba1bf09a163f32301745884e1b75d172d00`。本次未自动更新用户服务器。


## v0.1.3-rc.4 紧凑布局与导航验证（2026-11-18）

- 本地通过 Go race/vet、前端类型检查与生产构建、7 项 Vitest，以及 16 项安装器检查。文件回归覆盖保护路径、符号链接/硬链接别名、过滤后分页、搜索、收藏及最近访问；Linux bind mount 过滤在发布 CI 验证。
- 22 项 Playwright 浏览器回归全部通过，覆盖路由代码加载失败重试、HTTP/HTTPS Cookie 兼容、全部侧栏菜单和资源、桌面/移动布局、文件修订冲突、标签/筛选保留、慢接口独立加载、后台暂停轮询和旧历史清理。
- 本机生产构建连续 20 次已缓存页面切换 P95 约 50ms（目标 ≤300ms）；模拟两秒接口延迟仍可导航。这是本机测试证据，不等同于公网首次加载耗时。
- 截图保存在 `.opsview/screenshots/compact-*.png`、`overview-desktop.png` 和 `overview-mobile.png`。本版无数据库迁移，未连接或自动更新用户服务器。实际 Linux 发布包导航和安装/回退以 GitHub 发布运行结果补充。


### rc.4 GitHub 发布证据

代码提交 `bdfc58c`、标签 `v0.1.3-rc.4`：[发布运行 44123553761](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44123553761) 全部成功。

- `build`：Go race/vet、7 项前端单元测试、16 项安装器检查、Linux 文件/挂载别名检查、22 项浏览器流程、Nginx 日志轮转与 PHP Session smoke 通过；生成发布包后再次使用包内 Linux 二进制通过 22 项浏览器流程，并完成真实 systemd 安装、更新及故障回退。
- `publish`：发布 `install.sh`、`opsview-linux-amd64.tar.gz` 和 `SHA256SUMS`，标记 prerelease。
- `verify-public-install`：从公开固定标签地址安装、登录、改端口并执行 `ov update` 通过。

未连接或自动更新用户服务器；用户可按根目录安装教程显式更新或退回 rc.3。

公开下载复核：安装脚本和 Linux 发布包均与公开 `SHA256SUMS` 一致；发布包 23,769,696 字节，SHA-256 为 `29aa87fde577bdda0eef3a0a99cecb4d7e12468427bfba5d5697b8a11ec3b3ab`。`releases/latest` 仍指向 v0.1.1，测试版需显式指定版本。

## v0.1.3-rc.5 数据库管理增量

新增数据库三标签、同步纳管、账号权限、凭据临时查看、表维护、回收恢复及 Redis 结构化键操作。详细范围与上限见 [数据库管理说明](database-management.md)。

本地隔离测试使用自建并自动清理的 `opsview-dbtest-*` 容器，未连接用户业务服务。MySQL 5.7/8.4.8、PostgreSQL 18.0、Redis 7 实际执行创建/授权/改密/加密备份/新库恢复和 Redis 五类数据操作；原生客户端测试通过隔离工具适配执行，不能冒充每个原生版本安装验收。PostgreSQL 测试使用实际挂载的 pg_hba.conf 并以业务账号验证只读/读写，覆盖非 public schema 导出及恢复。备份工具失败验证原库仍在；大键、二进制和过期时间变更验证 Redis 冲突保护。

Go 测试覆盖 schema 2 → 3 保留密文及关联、同实例重名拒绝、跨实例同名、凭据身份/TOTP/CSRF/no-store、回收专属备份旧接口保护和 RESP 限制。浏览器覆盖三标签表头、密码关窗清除、维护确认、Redis 编辑、窄屏和全站导航。CI 使用同一测试在临时 Ubuntu 主机执行，并对打包程序进行安装、更新、回退和菜单回归。未在用户服务器自动部署。

2026-11-18 发布验证完成：[`44218209436`](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44218209436) 的 build、publish、verify-public-install 全部成功；发布提交为 `7ab4265`。24 项浏览器回归及真实 Linux 数据库集成、systemd 安装更新回退、公开 Release 下载安装通过。本地另用正式 Linux amd64 发布二进制在隔离 Ubuntu 容器执行 24 项页面测试，全部通过，缓存导航 P95 51.5ms。

公开附件摘要：

```text
82ebdc037acef77758de8551af2e65dbc5fa212780dee8cc2682f7684385c3e8  opsview-linux-amd64.tar.gz
3b6d03e936c6475109f87f7caed6d5ec2af4257c708eb79f20cd851d3733fc40  install.sh
```

## 2026-11-18 rc.6 网站创建

- 三类专用建站弹窗、域名自动目录、手动路径保留、发送 Host、顶部统计/基础扫描/安全/Nginx 操作；旧站点根目录不迁移。
- 网站内 `managed` 进程使用非 root systemd 服务；默认独立用户，JSON argv 不通过 Shell；运行失败和后续网站应用失败有服务配置回退。
- 本地 Go race/vet、前端类型检查与构建、7 项 Vitest、安装器 16 项通过。浏览器新增目录生成、托管参数和 Nginx 确认检查，完整套件共 26 项。
- 已加入 `TestLinuxWebsiteCreation`，在显式允许的临时 GitHub Ubuntu 24.04 systemd 主机执行，使用发行版 Nginx/PHP 验证新根目录、真实 HTTP、PHP、非 root 进程、失败回退、到期停用及生产 umask。该测试不替代每个软件版本的安装矩阵。
- 发布及公开安装工作流通过后补充最终运行记录。未自动更新用户服务器。

rc.6 发布前复核：生产 Linux amd64 包在隔离 Ubuntu 容器执行 26 项 Playwright 全部通过（含移动端建站弹窗、目录联动、托管提交和 Nginx 操作确认），缓存导航 P95 67ms。新增失败部署清理回归验证：存在未登记配置时拒绝清理；确认无服务现场后只释放目录占用，保留项目文件，重复操作幂等。

[Linux 网站检查 44233442676](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44233442676) 的 `websites` 与 `deployment` 已成功，覆盖真实 systemd 网站部署及安装回退；最终标签发布与公开安装仍以发布流水线结果为准。

### rc.6 发布验证完成

发布提交 `45411b4`、标签 `v0.1.3-rc.6`。[发布流水线 44234216247](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44234216247) 的 `build`、`publish`、`verify-public-install` 全部成功。

- Go race/vet、前端类型检查/构建/单元测试、安装器和 Linux 文件检查通过。
- 真实 systemd 主机的网站创建测试、数据库集成、日志轮转与 PHP Session 检查通过。
- 26 项浏览器回归通过；打包后再次用发布二进制通过全部页面测试。
- systemd 安装、更新及故障回退，以及公开附件安装、登录、改端口和 `ov update` 通过。
- 本地额外下载公开安装脚本并与公开 SHA256SUMS 核对一致；完整发布包公开下载与安装由 CI 验证。

公开附件摘要：

```text
dd1eb470aad2491b80b3a7b15cc94bcac5c81b5ca79a27c01c46bda9a4a893f8  opsview-linux-amd64.tar.gz
3b6d03e936c6475109f87f7caed6d5ec2af4257c708eb79f20cd851d3733fc40  install.sh
```

本版为预发布测试版，用户通过 `ov backup`、`ov update --version v0.1.3-rc.6` 显式更新。未自动更新用户服务器；后续验收文档追加不改写已公开标签及附件。


## 2026-11-18 rc.7 软件市场与多版本安装

- 筛选入口使用带图标的主按钮“显示全部软件”；核心软件默认选中已存在版本，已占用版本隐藏重复安装操作，手动选择其他版本后保持选择。已安装插件隐藏安装，保留设置/检查/卸载。
- 本地 Go race/vet、前端类型检查与生产构建、安装器 16 项检查通过。补充默认选中已有旧版本、目录先返回而资源后返回的回归后，重新构建实际 Linux 包，27 项 Playwright 全部通过，缓存菜单切换 P95 为 50.1ms。
- 新增安装目标为四个 MySQL 兼容版本、五个 PHP 版本、七个 Redis 版本 × 四系统，共 64 组合。隔离依赖、源码归档硬链接及时间戳、运行目录权限、PHP 构建辅助脚本重定位均纳入检查；PHP 7.0/7.2 的符号及 C/C++ 声明兼容补丁、旧 configure 在新版 GCC 上的真实能力探测也纳入实际构建。
- 组合必须先完成真实 systemd 启动及协议检查，再在独立干净系统完成二进制安装，才可提升目录；面板公开安装结果待发布流水线完成后补充。当前服务器未自动更新。


### rc.7 原生软件矩阵完成

[原生软件运行 44244762924，第 2 次尝试](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44244762924/attempts/2) 完成全部 64 个组合，构建提交 `ae59e6e511a29f5ce0a8683d427ab59add7c4f57`。第一轮仅 MySQL 5.7 / Ubuntu 22.04 因依赖源 HTTP 403 失败；只重跑该任务及发布任务后成功，原先 63 个通过任务及产物继续保留。每个组合包含真实 systemd/协议探测和独立干净系统安装，PHP 另验证常用内置扩展及 intl 实际格式化。

独立软件预发布 `software-44244762924` 已公开，包含 48 个 PHP/Redis 软件包、版本目录及总报告，共 50 个附件。16 个 MySQL 系统组合使用原有固定来源/摘要的官方包，不重新打包上游主软件。新增报告见 [原始验收 JSON](releases/v0.1.3-rc.7-native-acceptance.json)；加上先前 24 个组合，当前目录累计 88 个极速、64 个编译安装系统组合。

ncurses 另增加固定摘要的 GNU 镜像回退。已分别下载两个源，SHA-256 均为 `136d91bc269a9a5785e5f9e980bc76ab57428f604ce3e5a5a90cebc767971cc6`；Go race 测试验证 HTTP 403 后镜像下载、摘要错误不覆盖已校验文件、取消不再请求。添加镜像不改变已验收软件包内容及摘要，最终面板发布继续执行全量检查。

### rc.7 面板发布验证完成

发布提交 `e76a36e2a144e5c98990464d6935f624836bb849`、标签 `v0.1.3-rc.7`。[发布流水线 44249330265](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44249330265) 的 `build`、`publish`、`verify-public-install` 全部成功，各步骤无跳过。

- Go race/vet、前端类型检查/生产构建/单元测试、安装器、Linux 文件与真实网站部署检查通过。
- 27 项浏览器流程及真实数据库集成、日志轮转、PHP Session 检查通过；生成发布包后再次用包内二进制验证导航。
- 真实 systemd 安装、更新和故障回退，以及公开附件安装、登录、改端口和 `ov update` 通过。
- 独立下载公开 `install.sh` 并与公开 `SHA256SUMS` 核对；GitHub 附件摘要与校验清单一致。完整面板包公开下载和实际安装由 CI 验证。
- 独立下载公开 PHP 8.2.35 / Debian 12 极速包（10,677,900 字节），SHA-256 `661a8a7e6dff9dfd572e0e22e489acc670751e252b6cc01e072c4d664906cbe8` 与已提升目录一致。

公开面板附件摘要：

```text
2d0c143fa319303c0e06d8bdad29b8ff4fdf87622d7880259e94beb39a4cdd02  opsview-linux-amd64.tar.gz
3b6d03e936c6475109f87f7caed6d5ec2af4257c708eb79f20cd851d3733fc40  install.sh
```

本版为预发布测试版，使用 `ov backup`、`ov update --version v0.1.3-rc.7` 显式更新；未连接或自动更新用户服务器。更新不自动安装或升级业务软件，SQLite schema 保持 3。此验收记录追加不改写已公开标签和附件。

## 2026-11-18 rc.8 统一市场与 Nginx 安装

- 通过 Edge 只读查看宝塔软件商店的分类与列表；核心软件与插件合并，新增八分类及已安装实例行，保留仓库管理及 Docker 模板。没有实际条目的分类显示空状态。
- Go race/vet、前端类型检查和构建、7 项 Vitest、16 项安装器检查通过。新增多版本实例、孤立插件、分类筛选、带参数跳转和移动端回归；完整 Linux 发布二进制的 28 项浏览器结果待最终检查后补充。
- [Nginx 原生工作流 44268894751](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44268894751) 的 matrix、48 个安装组合和 publish 全部成功，软件提交 `4e64fa967bf185e427d6288309826536e0e0a13e`。每个组合验证真实 systemd、准确版本、可信证书 HTTPS、HTTP/2、正则重写及独立干净系统极速安装。
- 软件 Release `software-44268894751` 包含 48 个软件归档和两个元数据附件；已核对完整报告、48 个归档摘要及公开版本目录，累计 136 个极速、112 个编译组合。原始报告见 [rc.8 原生验收](releases/v0.1.3-rc.8-native-acceptance.json)。
- PCRE 三个上游镜像独立下载摘要一致，静态依赖只进入旧版 Nginx 实例并保留许可证。首次来源不稳定的工作流已取消，以修复镜像后的完整成功运行作为验收依据。
- 另独立下载公开 `nginx-1.22.1-debian-13.tar.gz`（503,313 字节），SHA-256 为 `251d8193d98bc9153a245c533753f979a62ad34761aeae749ff167c9c10f5ef9`，与目录、报告和 GitHub 附件摘要一致。

面板发布及公开安装验证完成后补充最终记录；未自动更新用户服务器。

rc.8 发布前复核：使用实际 Linux amd64 发布二进制在隔离 Ubuntu 容器完成全部 28 项 Playwright，全部通过，缓存导航 P95 67.1ms。覆盖八分类、同软件多个版本、已安装孤立插件、隐藏重复安装、仓库管理、Docker 模板、带参数跳转、移动端和全部侧栏。版本目录提升后再次通过 Go race/vet。公开 Nginx 归档及两个元数据附件摘要已独立核对。

### rc.8 面板发布验证完成

发布提交 `6c1800d7a5951b53b1d3932bb835b8fc4d3b8e74`，标签 `v0.1.3-rc.8`。[发布流水线 44271204423](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44271204423) 的 `build`、`publish`、`verify-public-install` 及全部步骤均成功。

- Go race/vet、前端类型检查/构建/单元测试、安装器和 Linux 文件检查通过。
- 真实 systemd 网站部署、MySQL/PostgreSQL/Redis 集成、日志轮转和 PHP Session 通过。
- 28 项页面回归通过，生成发布包后再次通过全部页面和导航检查。
- 真实 systemd 安装更新及故障回退、公开下载附件后的安装登录、改端口与 `ov update` 通过。
- 独立下载公开安装脚本及校验清单并核对一致；GitHub 附件摘要与清单一致。完整面板包公开下载及安装由 CI 验证。

公开面板包为 23,991,180 字节，摘要如下：

```text
31607b220cc652f7c64eca4645e082420cb8e68e305d91fcda31d70bc30c1585  opsview-linux-amd64.tar.gz
3b6d03e936c6475109f87f7caed6d5ec2af4257c708eb79f20cd851d3733fc40  install.sh
```

显式更新命令：`ov backup`、`ov update --version v0.1.3-rc.8`。本次未连接或更新用户服务器，不自动安装或升级业务软件。验收记录追加不改写公开标签及附件。


## 2026-11-18 rc.9 文件管理行内交互

- 合并路径输入和面包屑；失焦保留草稿且不跳转，显式提交后才变更实际目录。失败保留原列表，直接目录分段跳转、收藏、快捷入口和移动端继续可用。
- 目录大小按完整路径独立计算，原位显示等待、结果、重试和取消；按 1024 转换单位。已完成结果在登录会话内保留，文件变更使相关结果失效；晚到的旧结果不能覆盖变更后的状态。
- 备注失焦或回车行内保存，避免重复提交；未变更不请求，清空移除，Esc 取消，失败保留草稿；输入法组合期间的回车不提交。
- 本地已通过：6 项 Playwright（含全部侧栏、紧凑布局、文件编辑冲突、路径草稿、大小缓存/取消/失效及备注）、7 项 Vitest、前端类型检查/生产构建，以及 Go public/controller 测试。
- 发布前本地完整复核：`make api check`（Go 全量 race/vet、前端类型/构建、7 项 Vitest）、16 项安装器检查及全部 31 项 Playwright 通过；缓存菜单切换 P95 为 66.8ms。
- GitHub 完整发布检查和公开安装结果待本次工作流完成后追加。未连接或更新用户服务器；没有新增数据库迁移。


### rc.9 发布与公开安装验证完成

发布提交 `7ba8daf8f272c77ebe9835be1da99414403df6f9`，标签 `v0.1.3-rc.9`。[发布流水线 44279841917](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44279841917) 的 `build`、`publish`、`verify-public-install` 及全部步骤成功。

- Go 全量 race/vet、前端类型/构建和 7 项 Vitest、安装器及 Linux 文件检查通过；网站真实部署、MySQL/PostgreSQL/Redis 管理、日志轮转及 PHP Session 验证通过。
- 源码服务和实际 Linux 发布二进制分别完成全部 31 项 Playwright，均通过；真实 systemd 首次安装、正常更新和故障回退通过。
- GitHub 临时 Ubuntu 24.04 从公开固定标签下载、校验、安装、登录、改端口和执行 ov 更新验证通过。
- [常规检查 44279804418](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44279804418) 的 check/deployment/websites、[候选构建 44279804498](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44279804498) 全部通过。
- 已在独立工作站从公开固定标签下载三个附件，核对摘要、bootstrap 与仓库一致、归档内四个二进制和安装/更新脚本、rc.9 版本及发布说明。首次本机网络超时后重试成功。

```text
9c2e3c1bb2cd947255f3211b97a299ae9fee76bf3efd8cfa7340e234a21d1a58  opsview-linux-amd64.tar.gz
3b6d03e936c6475109f87f7caed6d5ec2af4257c708eb79f20cd851d3733fc40  install.sh
```

更新：`ov backup`、`ov update --version v0.1.3-rc.9`。本次未连接或更新用户服务器；测试版不替换默认 latest，SQLite schema 保持 3，历史版本与业务数据保留。此验收追加不修改已公开标签及发布附件。

## 2026-11-18 rc.10 系统防火墙与 SSH 管理

- 只读核对参考服务器宝塔防火墙、SSH 模块的字段与页面动作，以 OpsView 的 Go 执行器和 Vue 组件独立实现；未更改参考服务器配置、服务或数据。实现范围及恢复方式见 [系统安全说明](system-security.md)。
- UFW 实际规则与启停、120 秒持久化确认回退；SSH 多端口、认证及 root 策略、修订校验、旧端口保留、明确确认和超时恢复；root 公钥指纹/导入/删除与最后公钥保护；登录及现有 fail2ban 诊断。
- 发布前开发验证：Go 全量 race/vet、前端类型/生产构建、7 项 Vitest、33 项 Playwright 通过，缓存菜单切换 P95 为 83.3ms。
- 独立 Debian 12 容器的真实 OpenSSH 公钥登录、旧/新端口共存、确认移除旧端口、持久化超时回退、UFW 启用及回退通过。新增检查加入常规、候选与发布工作流；未在用户服务器执行安全变更。
- SQLite schema 保持 3。Ubuntu 等其他发行版新增安全功能的真实集成矩阵仍待完成；不能将 Debian 容器结果视为全系统验收。GitHub 发布及公开安装验证结果完成后追加。

### rc.10 发布与公开安装验证完成

发布提交 `61a99c64457fac2aa3132149a1b41466787ecb3b`，标签 `v0.1.3-rc.10`。[发布流水线 44319238042](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44319238042) 的 build、publish、verify-public-install 全部成功。

- Go 全量 race/vet、前端类型/构建、Vitest、安装器、Linux 文件与真实网站部署、真实 SSH/UFW、MySQL/PostgreSQL/Redis、日志轮转和 PHP Session 检查通过。
- 完整 33 项 Playwright 通过，生成发布包后再次用实际 Linux 二进制完成浏览器回归；真实 systemd 安装、更新和故障回退成功。
- 临时 Ubuntu 24.04 从公开附件下载、校验、安装、登录、修改端口和运行 `ov update` 成功。
- 独立工作站下载三个公开附件，核对 SHA-256、安装脚本与仓库一致、归档内四个二进制及 rc.10 版本和发布说明。首次普通下载链接超时，重新解析公开固定标签附件后校验通过。

初始常规检查及候选构建在离线四系统测试失败，原因是新增测试夹具依赖最小镜像未安装的 Python，非面板运行代码故障。修正提交 `3e77949` 将命令模拟改为 Go 测试程序自身，不修改运行代码或公开附件，并把四系统离线测试加入后续发布门槛。本地 Ubuntu 22.04/24.04、Debian 12/13 离线测试及相关 Go race/vet 均通过。[修正后常规检查 44321855738](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44321855738) 的 check、deployment、websites，以及 [候选构建 44321855827](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44321855827) 全部成功，包括四系统离线测试、完整浏览器、实际包内二进制及 systemd 安装更新回退。修正仅涉及测试与后续发布门槛，公开版本的运行代码及附件保持不变。

公开面板包为 24,154,163 字节：

```text
00ec0b8f260ecbbaf05288f9fffa1a890692e63f6f8bb14c3740f7cae4af9ef3  opsview-linux-amd64.tar.gz
3b6d03e936c6475109f87f7caed6d5ec2af4257c708eb79f20cd851d3733fc40  install.sh
```

更新：`ov backup`、`ov update --version v0.1.3-rc.10`。本次未更新用户服务器或业务软件；更新本身不启用防火墙或修改 SSH。SQLite schema 保持 3。已确认的主机安全修改不会因降级面板自动撤回，降级前需处理待回退记录，详见运维手册。

## 2026-11-19 rc.11 统一系统防火墙

本轮新增 UFW/firewalld 统一适配器、四类真实规则、继承编辑、导入预览与差异同步、地区集合、Fail2ban 和本机事务恢复；SQLite 增量升级 schema 4。未更新用户服务器，未修改宝塔参考服务器。

本地验证：

- `go test -race ./...`、`go vet ./...` 通过，含规则解析、修订漂移、注入、事务互斥、导入未知字段拒绝、地区下载失败保留及任务去重测试。
- Vue/TypeScript 生产构建、7 项 Vitest、34 项 Playwright 通过；缓存导航 P95 66.7ms。浏览器测试覆盖自动继承、四标签、编辑回填、导入预览及勾选同步。
- 安装脚本摘要、归档边界、失败下载及参数防注入检查通过。
- 独立 Docker 网络命名空间使用真实 UFW/firewalld、IPv4/IPv6、独立客户端 TCP/Ping、本机 REDIRECT、远程 DNAT/MASQUERADE、地区过滤、管理来源保护与超时回退、Fail2ban 实际日志封禁/解封。测试不挂载 Docker Socket 或宿主机系统目录。

测试发现并修复：UFW 内部限速链误识别、IPv6 插入偏移、末条插入、firewalld 默认区域禁 Ping 不生效、默认区域 active 文字差异及旧版 firewalld 重载清空外部链。产品在存在外部链时拒绝 firewalld reload/启停，测试明确断言此保护，并仅在移除测试自建哨兵后验证允许的重载。容器内置 DNS NAT 仅在隔离夹具初始化时移除，不在产品中放宽保护。

本地 Linux arm64 隔离容器已分别通过 Ubuntu 22.04/24.04、Debian 12/13 的 UFW/firewalld 测试；新增真实出站阻断及删除恢复在 Ubuntu 24.04 firewalld、Debian 13 UFW 通过。

四系统 Linux amd64 矩阵、实际发布二进制、systemd 安装/更新和公开附件检查由 GitHub 候选/发布工作流继续验证；最终结果以对应工作流结论为准。隔离容器测试不能替代整机重启、独立公网、云安全组及所有 Docker/自定义防火墙组合验收。

升级与旧版完整元数据恢复见 [运维说明](operations.md#rc11-系统防火墙更新与回退)。发布后使用 `ov backup`、`ov update --version v0.1.3-rc.11`；不能只装旧二进制退回 schema 3。

### rc.11 发布与公开安装验证完成

发布提交 `c96b5d5c7342b5c422fe4dc1a2fbcf58cdfbf5a9`，标签 `v0.1.3-rc.11`。[发布流水线 44352698545](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44352698545) 的 build、publish、verify-public-install 全部成功。

- Go 全量 race/vet、前端类型检查、生产构建、7 项 Vitest、安装器及 Linux 文件检查通过；真实 SSH/UFW、网站部署、MySQL/PostgreSQL/Redis、日志轮转和 PHP Session 检查通过。
- Linux amd64 的 Ubuntu 22.04/24.04、Debian 12/13 均完成 UFW/firewalld 独立客户端实际流量测试，含 IPv4/IPv6、出站规则、转发、地区过滤、管理连接回退及 Fail2ban 封禁/解封。
- 源码服务及实际发布二进制分别完成 34 项 Playwright；源码服务缓存导航 P95 为 100ms。真实 systemd 安装、更新和故障回退通过。
- 临时 Ubuntu 24.04 从公开固定版本附件下载、校验、安装、登录、改端口和执行 `ov update` 通过。
- [常规检查 44352681678](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44352681678) 的 check、deployment、websites 和 [候选构建 44352681847](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44352681847) 全部成功。
- 独立工作站读取公开 Release 元数据及校验清单，下载并核对安装脚本摘要和仓库内容一致。普通附件链接存在连接超时；通过公开附件 API 下载脚本成功，完整面板包的公开下载及摘要、安装验证以该发布流水线为依据，不将工作站的部分下载视为成功。

公开面板包为 24,312,223 字节，GitHub 附件摘要与发布校验清单一致：

```text
a61c677c3de6e3608bc0719d47cf8504c21b2d022cb5beaca755315608942df8  opsview-linux-amd64.tar.gz
3b6d03e936c6475109f87f7caed6d5ec2af4257c708eb79f20cd851d3733fc40  install.sh
```

更新：`ov backup`、`ov update --version v0.1.3-rc.11`。本次未更新用户服务器或业务软件；整机重启、公网入站和云安全组仍需部署环境验收。SQLite schema 4 降级必须恢复升级前完整元数据，不能仅更换二进制。验收记录追加不修改公开标签和发布附件。

## 2026-11-19 rc.12 防火墙开关与继承端口修复

只读检查用户服务器发现，UFW 正常启用但存在宝塔遗留 `IN_BT` INPUT 跳转，旧兼容性判断因此锁死全部操作。本轮改为区分 UFW 生命周期限制与普通规则能力，在确认 `MANAGE_BUILTINS=no` 后保留外部链并允许管理；不自动修改系统默认配置。另修复 firewalld 的 `@port` 内部标记误判为服务附件，避免普通业务端口编辑被错误要求管理连接确认。

- Go 全量 race/vet、前端类型检查和生产构建、7 项 Vitest、16 项安装器检查已通过。
- 本地 Ubuntu 24.04 Linux arm64 隔离环境，真实 UFW/firewalld 均通过继承端口编辑、删除及独立客户端流量验证；UFW 真实关闭、开启及确认后，IPv4/IPv6 的 IN_BT 和 Docker/NAT 哨兵仍保留。管理来源保护、超时回退、转发、Ping、地区过滤和 Fail2ban 实际封禁/解封通过。
- 浏览器新增开关启停、继承端口编辑删除、firewalld 双栈普通端口、管理端口删除来源确认测试；最新完整结果及 GitHub 发布结果完成后追加。
- 未更新用户服务器，未修改其防火墙或服务。隔离容器测试不代表整机重启、公网入站、云安全组及全部自定义链组合验收。

SQLite 保持 schema 4。更新前执行 `ov backup`，更新使用 `ov update --version v0.1.3-rc.12`；涉及管理通道的操作仍需在 120 秒内重新连接并确认。恢复要求见 [运维说明](operations.md)。

最新前端构建后完整 37 项 Playwright 通过（1.4 分钟），含本轮全部防火墙交互；缓存导航 P95 为 66.6ms。相关 Go hostfirewall/executor race/vet 在最后的 firewalld 修正后再次通过。GitHub 四系统 Linux amd64、实际发布二进制及公开安装验证仍待发布流水线完成。

### rc.12 发布与公开安装验证完成

发布提交 `3dd4ae38edfdf6d0582e4d9581637aecd3776deb`，标签 `v0.1.3-rc.12`。[发布流水线 44443016290](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44443016290) 的 build、publish、verify-public-install 全部成功。

- Go 全量 race/vet、前端类型检查和生产构建、7 项 Vitest、安装器、文件及 SSH/UFW 回退检查通过；四系统离线测试、真实网站、MySQL/PostgreSQL/Redis、日志轮转和 PHP Session 检查通过。
- Linux amd64 的 Ubuntu 22.04/24.04、Debian 12/13 均完成 UFW/firewalld 独立客户端流量测试，含继承端口编辑删除、IPv4/IPv6、出站规则、转发、地区过滤、管理来源保护、回退和 Fail2ban；UFW 实际关闭再开启仍保留外部链及原规则。
- 源码服务与实际发布二进制分别完成全部 37 项 Playwright；真实 systemd 安装、更新和故障回退成功。临时 Ubuntu 24.04 从公开固定标签下载、校验、安装、登录、改端口和执行 `ov update` 成功。
- [常规检查 44443006130](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44443006130) 的 check、deployment、websites 及 [候选构建 44443005884](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44443005884) 全部成功。
- 独立工作站下载公开安装脚本和校验清单，核对摘要及脚本与仓库一致；完整面板包下载超时，不把部分下载视为成功。完整公开下载及安装验证以发布流水线为依据。

公开面板包为 24,316,887 字节，附件摘要与公开校验清单一致：

```text
6355f4300ff136431a0402c3f4bfc96a7d62ded67c0dd45d564716b09623309d  opsview-linux-amd64.tar.gz
3b6d03e936c6475109f87f7caed6d5ec2af4257c708eb79f20cd851d3733fc40  install.sh
```

更新：`ov backup`、`ov update --version v0.1.3-rc.12`。本次未自动更新用户服务器或修改其防火墙；四系统隔离测试不替代整机重启、公网入站和云安全组验收。SQLite 保持 schema 4；历史发布包及标签不变，本次验收追加不修改已公开标签或附件。


## 全站 UI 规范整改（2026-11-19，本地验证）

- 按根目录 `AGENTS.md` 接入公共弹窗和抽屉：固定视口内高度、固定头尾、独立内容滚动；普通弹窗和短弹窗使用集中维护的尺寸规格。
- 软件、网站、数据库、Docker、插件、文件、备份、SSH、防火墙和任务详情统一控件密度、表格、工具栏及操作区；软件与网站设置共用侧栏布局。保存、取消及确认操作保留在底部，移动端自动换行。
- 类型检查与生产构建通过，7 项 Vitest 和 38 项 Playwright 全部通过。测试运行真实本地 GoFrame 服务及嵌入的生产前端，覆盖长内容滚动、错误提示、标签切换、嵌套弹窗、焦点恢复、Esc 取消、抽屉关闭及小屏适配；全侧栏导航无缺失静态资源。
- 桌面和移动端截图已检查；缓存导航 P95 为 67.4ms，仅代表本地测试环境。构建资源已同步至 `server/public/dist/`。
- 本节为发布前本地记录，暂无数据库迁移；GitHub 发布与公开安装结果在通过后追加，未更新用户服务器。


### rc.13 发布前检查

版本号已统一为 `v0.1.3-rc.13`；本地 Go 全量 race 测试与 vet、前端类型检查/构建、7 项 Vitest 和 16 项安装器检查通过。UI 功能的 38 项 Playwright 已通过，GitHub 将对最终版本及实际 Linux 发布二进制再次执行完整页面、安装和更新回退验证。最终发布结果通过后追加。

### rc.13 发布与公开安装验证完成

发布提交 `c70ed3a2a85c028bd285c6d67086cb587f302165`，标签 `v0.1.3-rc.13`。[发布流水线 44450939548](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44450939548) 的 build、publish、verify-public-install 全部成功，总耗时 29 分 46 秒。

- Go race/vet、前端类型检查与生产构建、7 项 Vitest、安装器及文件检查通过；真实 SSH/UFW 回退、网站部署、MySQL/PostgreSQL/Redis、日志轮转及 PHP Session 检查通过。
- Linux amd64 的 Ubuntu 22.04/24.04、Debian 12/13 完成离线测试和 UFW/firewalld 独立客户端流量验证，覆盖继承规则、IPv4/IPv6、转发、地区过滤、管理来源保护、回退及 Fail2ban。
- 源码服务和最终 Linux 发布二进制分别完成全部 38 项 Playwright（1.9 分钟、1.8 分钟），真实 systemd 安装、更新与故障回退通过。
- 临时 Ubuntu 24.04 从公开固定版本附件下载、校验、安装、登录、改端口和执行 `ov update` 成功。
- 已从公开 Release 页面确认 `install.sh`、`opsview-linux-amd64.tar.gz`、`SHA256SUMS` 三个附件存在及其发布摘要。完整附件的公开下载、摘要和安装验证以发布流水线为依据，不将本地网络失败的下载视为成功。

公开 Release 页面显示的附件 SHA-256：

```text
04340a2c034f3689edc7499f798c6c1330c47f44a6be4fc8fcec65ee81151f0a  opsview-linux-amd64.tar.gz
3b6d03e936c6475109f87f7caed6d5ec2af4257c708eb79f20cd851d3733fc40  install.sh
60119e3790d59f3d12cf8d5da7e60a94141c53a67c76d031d49ff5e23b894a1f  SHA256SUMS
```

更新：`ov backup`、`ov update --version v0.1.3-rc.13`。本版为预发布测试版，不改变正式 latest 入口；SQLite 保持 schema 4，没有新增数据库迁移。本次未自动更新用户服务器；整机重启、公网入站及云安全组仍需部署环境验收。验收记录追加不移动已公开标签、不覆盖发布附件。回退需恢复更新前配套程序、运行配置及元数据，详见 [运维说明](operations.md)。


## 2026-11-19 rc.14 概览 GitHub 入口（发布前检查）

概览右上角新增 GitHub 图标，在新标签页打开项目仓库；提供悬浮说明、键盘焦点及移动端 44px 点击区域。版本、OpenAPI、安装和更新文档统一至 `v0.1.3-rc.14`。本地 Go 全量 race/vet、前端类型检查与生产构建、7 项 Vitest、16 项安装器检查通过；桌面/移动端布局和导航相关 2 项 Playwright 通过并检查截图。无数据库迁移，SQLite 沿用 schema 4。最终 Linux 包及公开安装结果通过后追加；本次不自动更新用户服务器。


### rc.14 发布中止及 rc.15 测试修正

[rc.14 发布构建 44456308309](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44456308309) 的页面测试为 37 项通过、1 项失败，尚未公开 Release。失败是小屏 MessageBox 测试测到约 1.5px 位移：测试只等待自身和子节点动画，而 Element Plus 在外层 `.el-overlay-message-box` 上执行入场位移。修正测量辅助函数，使其同时等待祖先元素的有限动画，保留原有 1px 固定布局断言。使用新标签 `v0.1.3-rc.15` 重新执行发布验证，不移动 rc.14 标签，不跳过检查。

修正后小屏 MessageBox 用例连续运行 10 次通过；重新构建 rc.15 前端后，全部 38 项 Playwright 通过（1.5 分钟），缓存导航 P95 为 66.7ms。最终 Linux 包及公开安装结果由 rc.15 发布流水线继续验证。


### rc.15 发布与公开安装验证完成

发布提交 `e1b22e85b5a9d97638a335766f294b3e905da38c`，标签 `v0.1.3-rc.15`。[发布流水线 44458149097](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44458149097) 的 build、publish、verify-public-install 全部成功，总耗时 23 分 59 秒。rc.14 没有公开 Release，原标签保留。

- Go race/vet、前端类型检查、生产构建、7 项 Vitest、安装器、文件及真实 SSH/UFW 回退检查通过。
- 四系统离线测试、真实网站部署、MySQL/PostgreSQL/Redis、日志轮转、PHP Session 以及 Ubuntu 22.04/24.04、Debian 12/13 的 UFW/firewalld 独立客户端验证通过。
- 源码服务和最终 Linux 发布二进制分别完成全部 38 项 Playwright，包含修正后的小屏弹窗固定布局测量；真实 systemd 安装、更新及故障回退通过。
- 临时 Ubuntu 24.04 从公开固定标签下载、校验、安装、登录、修改端口并执行 `ov update` 成功。
- 已从公开 Release 页面确认三个发布附件存在及其摘要。完整附件公开下载与安装验证以发布流水线为依据，本机访问 GitHub 偶有超时，不将部分下载视为成功。

公开 Release 页面显示的附件 SHA-256：

```text
324eaacbefd80db6e6d9370be9cd9b9fdb5c008a0fbd1deb216bb4d8c05c1de1  opsview-linux-amd64.tar.gz
3b6d03e936c6475109f87f7caed6d5ec2af4257c708eb79f20cd851d3733fc40  install.sh
83b2b1df43811f69a8716030db0848854487bd7131a23d2a5531051c485cf5b8  SHA256SUMS
```

更新：`ov backup`、`ov update --version v0.1.3-rc.15`。本版为预发布测试版，不改变正式 latest 入口；SQLite 保持 schema 4，无新增数据库迁移。本次未更新用户服务器。整机重启、公网入站及云安全组仍需部署环境验收；回退需要恢复更新前配套程序、运行配置及元数据，详见 [运维说明](operations.md#rc15-概览-github-入口)。本次记录追加不修改已公开标签或附件。

## 2026-11-19 rc.16 加载、SSH 与备份计划（发布前检查）

移除概览顶部任务入口，导航先呈现、内容局部加载；新增 SSH 受控服务操作、系统账号及结构化登录记录，并完善备份历史与计划管理。参考宝塔页面只读查看，没有修改参考服务器配置；系统账号、停用恢复等写入验收均在隔离 Linux 容器完成。

- 最终 `make api check` 通过：Go 全量 race、vet，前端类型检查、生产构建、7 项 Vitest。
- 安装器 16 项检查通过；全部 41 项 Playwright 通过（1.7 分钟），缓存导航 P95 66.8ms，覆盖慢请求加载、固定弹窗、账号验证与清理、计划复制和执行。
- 隔离 Linux 验证真实账号创建、密码修改、锁定/解锁及受管删除，真实 SSH 停用、超时自动恢复与恢复后的公钥登录，以及 SSH 配置和 UFW 回退。
- 新增任务列表回归：即使操作 payload 不可解密，列表仍从独立短关联读取计划 ID，避免解密大文件内容；任务执行仍拒绝损坏 payload。

本地检查通过不代表公开发布及生产更新完成；实际发布包、四系统矩阵、公开安装及线上替换结果完成后追加。SQLite 保持 schema 4，更新前备份；本轮用户已明确授权发布并替换现有线上面板。

### rc.16 发布与线上替换完成

发布提交 `4fb0f4f710f1a10b828ead57fc0ebdcbea1e47db`，标签 `v0.1.3-rc.16`。[发布流水线 44466353065](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44466353065) 的 build（24 分 1 秒）、publish 和 verify-public-install 全部成功。

- Go race/vet、前端类型检查、生产构建、单元测试、安装器、文件、真实 SSH/UFW 及四系统离线检查通过。
- 真实数据库、网站、PHP Session、日志轮转及 Ubuntu 22.04/24.04、Debian 12/13 的 UFW/firewalld 独立客户端矩阵通过。
- 源码和最终 Linux 发布二进制的页面测试通过；systemd 安装、更新及故障回退通过。
- 临时 Ubuntu 24.04 从公开 Release 下载、验证摘要、安装、随机账号登录、修改端口、HTTP 切换及 `ov update` 通过。
- 从公开 Release 页面核对三个发布附件及摘要；保持预发布，不改变正式 latest。

公开附件 SHA-256：

```text
3595355a05d95f905cf98297d1df80f62b8a4fc46aabbdaf33ba5b9d1f22f7cd  opsview-linux-amd64.tar.gz
3b6d03e936c6475109f87f7caed6d5ec2af4257c708eb79f20cd851d3733fc40  install.sh
9e5f920532760371d0e7020d347439770689bcfd01066580e3d5ba2eac650732  SHA256SUMS
```

按本轮用户授权，现有线上面板于 2026-11-19 14:04（Asia/Shanghai）通过 `ov update --version v0.1.3-rc.16` 从 rc.15 更新至 rc.16；下载、校验、备份、管理员保留和连续三次健康检查通过。升级前加密配置备份为 `/var/lib/opsview/panel/backups/20261119T053646.874990760.ovp`（0600），完整回退快照为 `/var/lib/opsview/updates/20261119T060415Z-3141036`。

线上检查确认两项服务 running、重启计数 0、更新后无错误级别 journal，外网健康接口返回 rc.16；执行器只读验证 SSH 服务运行及受控能力、系统账号和结构化登录记录。升级快照与当前数据库中的资源 ID、类型和名称一致，原管理员保留。生产环境没有执行 SSH 停用或账号改密测试；相关写入测试证据来自隔离环境。线上浏览器既有会话的交互未纳入本次生产检查，页面流程以实际发布二进制的 Playwright 验证为据。

线上静态检查：通过本机回环逐个完整读取 41 个 JavaScript/CSS 依赖并核对 MIME，九个侧栏路由入口均返回当前打包应用；外网 HTML、5 个入口资源响应头和健康接口检查通过。开发机从公网并发完整下载所有资源曾遇到读取超时，不将该次批量下载视为成功，也不将本地缓存导航指标当作公网下载耗时。

## 2026-11-19 rc.17 备份、计划与设置验收

- 本地通过 `make check`：Go 全量 race、vet，前端 TypeScript、生产构建和 7 项 Vitest；16 项安装器检查通过。
- Playwright 44 项通过，新增脚本模板修订、暂停创建、root 身份确认、设置冲突保留草稿、通知标签独立读取、归档任务结果与失败提示；现有登录、导航、文件、数据库、SSH、防火墙及固定弹窗回归保留。缓存切换 P95 67ms（本地生产资源）。工具栏截图复核后补充筛选宽度约束，最终发布包再次回归。
- 后端新增验证覆盖：root 授权绑定内容、重新启用与修改内容重新验证、配置修订/回退、恢复暂停计划及撤销令牌、独立历史/日志清理、资源锁与跳过、HTTP 地址校验、BasicAuth/mTLS 附加认证、API 范围/HTTPS、Passkey 方法限制、通知签名/平台响应、持续阈值/冷却/恢复。
- S3 使用真实 AWS SDK 对接本地 HTTP S3 协议夹具，验证远端取回、摘要、上传失败保留及远端保留清理；这不等同于各供应商生产 Endpoint 验收。没有向用户通知渠道发送测试消息。
- 发布流水线新增隔离 Linux systemd 脚本实际退出码、秘密遮盖、控制组取消，以及 root 所有归档通过 `ov restore` 验证/切换、恢复后登录、网页入口单独启停。流水线结果在运行后单独登记。
- SQLite schema 5；成功升级后的降级要求恢复配套元数据快照，不能只替换旧二进制。实际供应商 S3、浏览器硬件 Passkey、公网 ACME、真实用户通知平台及完整灾难恢复仍需按目标环境演练。

此版只发布测试包，不自动更新生产服务器。历史记录中的“单 worker”和“无告警渠道”描述已由 rc.17 的四 worker 资源锁及通知实现取代。

### rc.17 公开发布结果

标签 `v0.1.3-rc.17` 对应提交 `0db0407`。[发布流水线](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44500482713) 的 build、publish、verify-public-install 全部通过，已公开 Linux amd64 测试包、SHA256SUMS 和安装脚本。

- 四种 Linux 的离线测试、SSH/UFW 回退、UFW/firewalld 独立客户端矩阵、真实数据库、日志轮转及 PHP Session 回归全部通过。
- 真实 systemd 脚本验证退出码 0/7、私密变量遮盖和取消后的控制组清理。源码服务与发布包各自的 44 项 Playwright 均通过。
- 实际安装包验证 root 所有归档的本机恢复、新目录核对、恢复切换后登录、证书保留、单独启停网页入口、正常更新和故障回退。
- 公开安装入口验证固定标签下载、摘要、安装、登录和 `ov update`。本次没有连接或更新生产服务器。

这些结果不扩大上文列出的 S3 服务商、硬件 Passkey、真实通知平台及公网环境验收范围。

## 2026-11-19 rc.18 全站主题配色重构（发布前检查）

- 统一中性背景、文字、边框、侧栏、主操作及选中状态的语义色；浅色使用白色/浅灰，深色使用蓝灰层级，默认蓝色主色。已有自定义颜色按当前主题生成完整色阶并调整显示对比度，保留原始保存值。
- 适配表格、按钮及悬停、错误/成功/警告状态、Teleport 弹窗/下拉、编辑器、日志和概览图表；人工复核浅色/深色概览、设置弹窗、移动端和深色配置编辑器截图。
- 默认及恢复默认改为浅色，保留已保存的深色/跟随系统选择。设置弹窗加载时禁止编辑和预览，避免迟到的响应覆盖输入草稿；延迟接口、409 草稿保留及取消主题预览已回归。
- TypeScript 检查和生产构建通过；13 项 Vitest 通过，包含默认/极端自定义颜色、两种模式的文字及操作对比度、错误提示色阶和非法颜色回退。
- 49 项 Playwright 全部通过（2.0 分钟），覆盖全部侧栏、现有业务流程、主题预览和取消、系统模式变化、深色编辑器、错误状态及固定弹窗；本地生产资源缓存切换 P95 67.5ms。
- `go test ./internal/preferences ./public` 通过，验证默认设置及打包资源。本版不增加数据库迁移；以上为发布前本地结果，后续发布与线上验收见下文。不把本地指标视为公网网络耗时。

### rc.18 公开发布与线上更新完成

标签 `v0.1.3-rc.18` 对应提交 `919dbc8`。[发布流水线 44542712860](https://github.com/gzdzh-cn/dzh-OpsView/actions/runs/44542712860) 的 build、publish、verify-public-install 全部成功。公开测试包于 2026-11-19T14:50:37Z 发布，包含 Linux amd64 包、SHA256SUMS 和安装脚本。

- 本地最终 `make api check` 通过：Go 全量 race/vet、前端类型检查、生产构建和 13 项单元测试。CI 再次通过同类检查、四种 Linux 离线测试、真实数据库、SSH 回退、UFW/firewalld 独立客户端矩阵、systemd 脚本、日志及 PHP Session 验证。
- 源码服务和实际发布二进制的 49 项 Playwright 均通过；实际安装、配套快照回退和公开下载后的 HTTP/HTTPS 登录及 `ov update` 验证通过。
- 按用户授权，生产面板于 2026-11-19 22:52:57（Asia/Shanghai）通过 `ov update --version v0.1.3-rc.18` 从 rc.17 更新，下载、摘要校验及连续三次健康检查通过。保持 schema 5。
- 升级前加密备份：`/www/backup/opsview-panel/20261119T143002.968917007.ovp`，权限 0600；已使用 `ov restore --file` 成功恢复到独立核对目录 `/var/lib/opsview/restore-verified-621146222/panel`，未切换线上数据。
- 备份前发现 `/www/backup` 为 root:root 0600，面板用户无法穿越；保存原属性至 `/var/lib/opsview/updates/rc18-backup-directory-before.json`，仅改为 root:opsview 0610，允许面板组穿越，不授予目录读取及写入权限。专用子目录保持 opsview:opsview 0700。
- 更新器完整回退快照：`/var/lib/opsview/updates/20261119T145253Z-3301277`（root:root 0700）；日志：`/var/log/opsview/install-20261119T145244Z-3300988.log`。回退须配套恢复同一快照的程序、运行配置、元数据及单元，操作见运维手册。
- 线上面板和执行器均 active，重启计数 0，更新后无错误级别 journal。完整读取并核对 43 个 JS/CSS 依赖的 MIME，全部侧栏入口及任务地址返回当前应用；认证状态接口正常，未登录业务接口返回 401。
- 当前与升级快照的资源 ID/类型/名称、管理员记录及主密钥逐项相同，原两条实例保留。公网 Chromium 登录页可见，浅色背景和统一蓝色主色生效，无 pageerror 或缺失资源；截图保存在本地 `.opsview/screenshots/rc18-production-login.png`。没有修改生产管理员凭据或尝试实际账号登录，凭据登录回归以隔离环境为证。

## 2026-11-19 rc.19 行内设置、主题入口与终端菜单（本地验收）

- `make api check` 通过：Go 全量 race/vet、前端类型检查与生产构建、13 项 Vitest；补充运行的维护、认证、设置包 race 测试通过。安装器检查通过。
- Playwright 53 项全部通过（2.1 分钟），包含 HTTP/HTTPS 会话、全部侧栏、网站/数据库/文件/SSH/防火墙、固定弹窗及新功能。生产资源本地缓存切换 P95 66.6ms，不作为公网网络耗时。
- 新回归验证主题三选项、系统颜色变化、主题保存冲突不改变现有配色；新旧亮暗模式的下拉、弹窗、编辑器及移动端保持配色和固定头尾。
- 验证普通设置点击文本编辑并局部保存、延迟读取禁止输入、409 保留草稿、取消或 Esc、切标签取消过期读取、清除认证草稿、登录会话管理就地展开；隐藏名称/侧栏标题/界面标签。入口编辑保留其他访问值并走身份确认及 120 秒重新连接流程。
- 后端验证首次安装随机入口、初始化/更新保留原入口（包括关闭）、CLI 每次开启轮换、关闭仅返回成功、待确认配置拒绝并发入口修改、双列菜单零号首项。新旧写入接口均拒绝修改名称。密码通过但缺 TOTP 返回 428，不创建会话和不累计失败；密码错误与验证码错误仍受登录限流。
- 人工复核浅灰蓝侧栏、面板设置、主题覆盖层以及移动端告警标签截图。本版保持 schema 5；Linux 实际安装、公开发布和线上部署记录在完成后追加。

### rc.20 发布候选修订

rc.19 已推送候选标签，但未发布或更新生产；复查发现 Linux 部署验收脚本仍断言旧版“密码不保存”文字，且新入口读取未兼容 v0.1.0 无运行配置文件。rc.20 修正测试为核对密码遮盖值和完整入口链接，并在旧配置缺失时按未启用入口处理。保留 rc.19 标签身份，使用新标签发布；业务实现与上述已完成的 53 项浏览器验收一致，版本与发布文档同步更新。

### rc.21 最终候选

发布等待期间复查局部主题事件，修正其不含 refreshSeconds 时会把浏览器轮询重置为 15 秒的问题。仅显式携带刷新字段时调整计时器；主题仍同步应用。最终线上交付使用 rc.21，rc.19/rc.20 均未部署生产。

### rc.22 协议入口兼容

进一步验证默认安全入口与 SSL 开关组合，将入口 Cookie 按协议分离，避免 HTTP 覆盖已有 Secure Cookie 被浏览器拒绝。HTTPS 保留旧名称，HTTP 兼容旧入口；单元测试验证同浏览器两类 Cookie 共存、跨协议拒绝及轮换失效。隔离 Linux 安装增加同一 Chromium 上 HTTPS → HTTP → HTTPS、重新连接确认、登录及刷新回归。生产交付改为 rc.22，rc.19/rc.20/rc.21 均未部署生产。静态备份/时间同步说明行使用普通文字，避免无操作按钮。

rc.21 的 GitHub 发布任务因 proxy.golang.org 下载 sqlite 模块时 HTTP/2 INTERNAL_ERROR 中断，未执行发布及线上更新。rc.22 在原检查前增加保持校验的 Go 模块下载三次重试，下载阶段使用 HTTP/1.1；不更换依赖、校验和或跳过任何测试。本地 rc.22 的类型检查、生产构建和 54 项 Playwright 再次全部通过（2.2 分钟），缓存切换 P95 67.6ms；入口协议隔离及已有认证控制器的 race 测试通过。

### rc.23 发布脚本修正

rc.22 依赖下载步骤因重试循环缺少结束符而退出，未发布和部署。rc.23 补齐循环并对全部工作流的 Shell run 块执行语法检查；产品实现不变，继续完整发布验收。
