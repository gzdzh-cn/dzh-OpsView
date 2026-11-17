# OpsView

使用 GoFrame 2、Vue 3、TypeScript 和 Vite 构建的中文单机服务器运维面板。面板服务与特权执行器分离，元数据存放 SQLite，前端随 Go 二进制发布。

当前版本为 **0.1.3-rc.3 预发布测试版**。主要菜单和服务器操作已实现；生产验收尚未完成，具体测试证据及限制见 [验收记录](docs/acceptance.md)。

本轮软件管理、网站和面板设置增量以 `v0.1.3-rc.3` 发布测试。默认 latest 入口仍获取正式 Release；测试新功能请使用下面的固定版本命令。已验证范围和未完成项见 [软件管理实现说明](软件管理实现说明.md)。

本版统一全站业务列表，文件管理扩展至服务器根目录，移除应用进程入口并保留旧服务，增加 Docker 引擎状态与 API 协商；软件市场只提供原生极速/编译安装。继续验证内嵌资源及实际发布二进制的侧栏导航。

功能、架构、安装维护方式和当前限制的完整介绍见 [项目介绍](项目介绍.md)。

## 本地启动

需要 Go 1.27.3+、Node.js 22.12+（建议 Node.js 24）及 npm。

```bash
make dev
```

浏览器打开终端显示的 Vite 地址，通常为 `http://127.0.0.1:5173`。后端默认监听 `127.0.0.1:18790`。第一次登录需要 `.opsview/setup.token` 中的本机初始化令牌，然后创建唯一管理员。开发模式没有预设账号；Linux 新安装自动生成随机账号。

macOS 开发模式提供真实本机监控、认证、资源配置、SQLite、任务及审计。systemd、软件安装、Docker、防火墙、服务器文件等功能通过 Linux 执行器运行；执行器缺失时任务会明确失败。

也可运行构建后的独立面板，验证嵌入页面：

```bash
make build
./dist/local/opsview --dev --data .opsview
```

## Linux 部署

完整操作步骤见 [安装教程](安装教程.md)，包含首次登录、端口放行、更新和常见问题。

目标系统为全新 Ubuntu 22.04/24.04、Debian 12/13，发布架构为 Linux amd64。现有服务只检测，不自动覆盖或导入其数据。

在服务器使用 **root** 执行以下命令，自动下载 GitHub 最新正式 Release、校验发布包、安装依赖并启动服务，无需安装 Go、Node.js 或 Git：

```bash
if command -v curl >/dev/null 2>&1; then curl -fL --retry 3 -o install_opsview.sh https://github.com/gzdzh-cn/dzh-OpsView/releases/latest/download/install.sh; else wget -O install_opsview.sh https://github.com/gzdzh-cn/dzh-OpsView/releases/latest/download/install.sh; fi && bash install_opsview.sh
```

安装本次测试版（root 执行）：

```bash
if command -v curl >/dev/null 2>&1; then curl -fL --retry 3 -o install_opsview.sh https://github.com/gzdzh-cn/dzh-OpsView/releases/download/v0.1.3-rc.3/install.sh; else wget -O install_opsview.sh https://github.com/gzdzh-cn/dzh-OpsView/releases/download/v0.1.3-rc.3/install.sh; fi && bash install_opsview.sh --version v0.1.3-rc.3
# 已安装面板：
ov update --version v0.1.3-rc.3
```

成功后终端显示实际 HTTPS 地址、随机用户名和随机强密码，可直接登录；密码仅显示一次，不保存明文。输入 `ov` 打开中文维护菜单，忘记密码使用 `ov password`。安装脚本需要联网访问 GitHub 和发行版软件源。GitHub 无法连接时可以使用下方离线传包方式。

固定版本、更新已有安装：

```bash
bash install_opsview.sh --version v0.1.1
bash install_opsview.sh --update                   # 更新至最新正式 Release
bash install_opsview.sh --update --version v0.1.1  # 指定目标版本，先备份后更新
```

重复安装会被拒绝。更新前会备份面板数据、二进制及 systemd 单元；更新失败尝试恢复旧版本。自定义 systemd 配置请使用 drop-in 文件。版本降级不保证数据格式兼容，不应绕过健康检查或手动覆盖数据目录。

开发者也可以自行构建并上传发布包：

```bash
make release
# 将 dist/opsview-linux-amd64.tar.gz 上传到目标服务器
# 在目标服务器解压后执行：
sudo bash opsview-linux-amd64/deploy/install.sh
```

面板使用独立 HTTPS 端口 **18790**，首次生成自签名证书。需要时放行此端口并配置云厂商安全组，然后使用终端显示的随机账号直接登录。`ov info` 查看地址和用户名，`ov info --public-host panel.example.com` 修正访问地址，`ov port` 修改端口。可以在“面板设置”上传受信任的面板证书。

安装过程创建 `opsview-panel.service` 与 `opsview-executor.service`。面板以 `opsview` 用户运行，执行器以 root 运行，二者仅通过 `/run/opsview/executor.sock` 通信。完整目录、升级、备份和故障恢复步骤见 [运维手册](docs/operations.md)。

## 已提供的功能

| 菜单 | 实现内容 |
| --- | --- |
| 概览 | CPU、内存、磁盘、网络、负载，快捷入口和任务提醒 |
| 网站 | PHP/反代/其他项目三类列表，独立 FPM、运行目录、多路径代理、流量及到期策略；证书、配置历史、漂移检查及重载回退 |
| 数据库 | MySQL/PostgreSQL 多实例，建库与专用用户，修改密码及来源，SQL 导入和流式导出，逻辑备份、新库恢复、验证后迁移 |
| Docker | 未安装/停止/兼容状态与 API 1.41–1.47 协商，容器应用模板、引擎安装，容器启停、日志、资源快照，按摘要拉取镜像，管理网络与卷，受限 Compose 项目 |
| 软件与插件 | 统一逐行市场、原生极速/编译安装、自动名称及准确版本占用；四类软件实例设置、配置修订与回退；签名源、Redis/Adminer/S3 扩展及低权限 worker |
| 文件 | 服务器 / 目录、收藏/备注、流式分块上传/续传（2 GiB）、Range 下载、修订编辑、批量复制/移动、ZIP/TAR.GZ、权限及 30 天回收站 |
| 备份与计划 | 本地/S3 加密备份、摘要、保留策略、恢复至新目录或新库，Cron 备份与续期 |
| 系统与安全 | UFW/DOCKER-USER 规则、监听与发布端口、120 秒确认回退、服务与日志、审计 |
| 面板设置 | 五类设置行、管理员/TOTP/会话、HTTP/HTTPS、地址/端口/访问限制、120 秒重新连接确认、证书及加密备份 |

新增 Nginx/MySQL/PHP/Redis 共 28 个目标版本，保留既有 PostgreSQL、Node.js 和历史容器实例。首批极速安装已开放 Nginx 1.30.6、PHP 8.4.27、Redis 8.10.3、PostgreSQL 18.0、MySQL 8.4.13、Node.js 22.23.5 × 四系统，共 24 个 Linux amd64 组合，全部通过真实 systemd/协议及独立干净系统安装，对应证据见验收记录。其余组合按验证结果开放；完整版本与限制见 [软件管理实现说明](软件管理实现说明.md)。目录是项目快照，**不代表上游最新版本**。所有下载固定摘要，容器固定镜像摘要；业务软件不会自动升级，自定义目录不执行任意 root Shell 配方。

历史核心 Nginx 和数据库容器继续管理，使用 Linux 宿主机网络：数据库默认监听 loopback，多实例通过端口区分。Compose 与应用插件默认使用独立 bridge 网络。远程数据库访问必须明确开启并限源放行；Docker 发布端口与 UFW 分开处理。

## 工程结构

```text
server/
  cmd/                   # 面板、执行器、插件 worker、签名工具
  internal/api/          # OpenAPI 合同
  internal/controller/   # HTTP、会话、CSRF、输入校验
  internal/service/      # 执行器接口
  internal/logic/        # 任务、计划、业务约束、面板备份
  internal/dao/          # SQLite、加密元数据、任务与审计
  internal/executor/     # 参数化的 Linux 操作与 Unix Socket RPC
  public/dist/           # 嵌入前端资源
web/src/                 # Vue 3 + Pinia + Router + Element Plus
packages/                # 固定版本目录、插件清单、规范与例子
deploy/                  # systemd、安装及更新脚本
scripts/                 # 构建、合同生成与集成验证
```

REST API 前缀为 `/api/v1`，登录后可读取 `/api/v1/openapi.json`。`make api` 同步合同与前端类型。长操作进入任务中心；日志 SSE 支持 `Last-Event-ID` 续读。任务重启后标为中断，不自动重放。

## 开发与验证

```bash
make api
make check                         # Go race 测试、vet、前端测试及类型构建
cd web && npm run test:e2e          # 真实浏览器与 GoFrame HTTP 服务
# 返回仓库根目录，有可用 Docker 时：
node scripts/container-smoke.mjs
./scripts/linux-tests.sh ubuntu:24.04 debian:bookworm-slim
make release                       # Linux amd64 包与 SHA256SUMS
./scripts/test-installer.sh         # 下载、版本固定、摘要、归档及失败阻断检查
```

仓库推送 `vX.Y.Z` 标签后，GitHub Actions 自动测试、构建并发布包含 `install.sh`、`opsview-linux-amd64.tar.gz`、`SHA256SUMS` 的 Release；`vX.Y.Z-rc.1` 等版本发布为预发布，不进入默认安装入口。详见 [发布手册](docs/releasing.md)。

[插件与自定义版本规范](packages/README.md) · [运维手册](docs/operations.md) · [验收记录与发布门槛](docs/acceptance.md)

## 本版行为与维护

软件市场采用逐行列表，Nginx/MySQL/PHP/Redis 提供带左侧菜单的实例设置。新增 PHP 独立站点 FPM、网站三类列表、流量游标及到期策略；面板设置提供五个标签页、HTTP/HTTPS 与 120 秒重新连接确认。原有账号和 Docker 入口保留。

28 个目标版本已固定上游地址与 SHA-256；目录可见不等于全部系统组合通过验收。旧 PHP 隔离依赖组合会明确拒绝安装。源码编译使用低权限用户；已签名的预编译配方按系统选择，未发布的包不能选择安装。

当前已实现范围、未完成项和验证证据见根目录 [软件管理实现说明](软件管理实现说明.md)。GitHub Actions 的 **Build candidate (no production update)** 生成候选附件；**Publish verified native packages** 在四系统分别构建、用真实 systemd 验证并在干净系统复验运行包。两者不自动更新服务器或替换 latest。

软件日志新增时间/级别/站点筛选、归档清理和当前脱敏结果下载，Nginx 在线重开日志会核对实际进程描述符。其他软件归档要求对应服务先停止；尚无定时轮转保留策略。

PHP Session 提供文件、受管 Redis 和本机 memcached 三种存储。memcached 支持端口及可选 SASL 认证，保存前验证真实会话读写，重载后通过私有 FastCGI 检查有效配置；失败恢复原文件。需要预先安装本机 memcached 服务及对应 PHP 扩展，版本默认配置仅由新网站继承，现有网站可单独修改。

本轮六个推荐软件的 24 个极速组合均已验收；Nginx、PHP、Redis、PostgreSQL 同时开放四系统编译安装。MySQL、Node.js 使用固定摘要的官方二进制。新增版本的完整支持范围及剩余工作见 [软件管理实现说明](软件管理实现说明.md)。`ov port`、`ov ssl on/off` 会显示新地址，需在 120 秒内重新连接确认，未确认时禁止更新。

应用进程功能已下线：旧 `/apps` 跳转网站“其他项目”，旧服务和文件保留，关联网站改为外部应用，不再因网站到期停止旧进程。后端拒绝旧应用变更，待执行任务取消并审计。新项目可使用反向代理连接外部服务或 Docker。

软件实例名称由后端生成 `软件-准确版本`，同一准确版本只能保留一个实例（历史重复记录保留管理）。卸载保留数据与版本占用，明确“清理记录”后才能重装；不同版本可以并存。无已验证极速包时展示原因，不会在安装失败后更换方式。

文件操作由 root 执行器处理，面板仍以普通用户运行。运行时/设备目录只显示基础信息，面板密钥、认证库、内部元数据和关键目录整体操作受保护。文件内容及 `.env` 不进入日志或审计。文件功能、限制与回退步骤见 [运维手册](docs/operations.md#服务器文件管理)。
