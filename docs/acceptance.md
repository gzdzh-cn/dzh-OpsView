# 验收记录和发布门槛

当前目标版本：v0.1.3-rc.4；下方保留历史版本的实际验收记录。记录日期：2026-11-18。

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
