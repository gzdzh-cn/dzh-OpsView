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
