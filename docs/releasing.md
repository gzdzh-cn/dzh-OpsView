# GitHub 发布与一键安装

仓库：`gzdzh-cn/dzh-OpsView`，公开访问。部署机器从 GitHub Releases 下载编译好的 Linux amd64 包；不拉取源码编译，不需要 GitHub Token。

## 首次发布及后续版本

维护者在代码验证完成后推送版本标签：

```bash
make check
./scripts/test-installer.sh
git push github HEAD
git tag -a v0.1.3-rc.1 -m 'OpsView 0.1.3-rc.1 test release'
git push github v0.1.3-rc.1
```

GitHub Actions 的 `Publish Linux release` 工作流会执行 Go race 测试与 vet、前端测试与构建、安装器检查和 Playwright，再构建四个 Linux 二进制，并在临时 Ubuntu 24.04 主机以真实 systemd 验证首次安装、正常更新和失败回退。`scripts/test-deployment.sh` 仅允许在隔离 CI 主机执行。构建 Job 只有读权限，发布 Job 使用仓库自动提供的 `GITHUB_TOKEN` 写 Release，无需额外保存个人 Token。需要仓库启用 Actions，且组织策略允许工作流写 Release。

发布说明保存在 `docs/releases/<标签>.md`，版本号必须与 Go、前端、锁文件及 OpenAPI 一致。

rc.10 起，常规检查、候选及发布流水线均运行 `scripts/test-security-linux.sh`：独立 Debian 12 容器中启动真实 OpenSSH，以公钥验证端口切换和回退，并在隔离网络中验证 UFW 启停及恢复。不使用宿主机网络或 Docker Socket；其他发行版的新增安全功能仍需独立验证。

构建成功后才创建草稿、上传完整产物、发布 Release。工作流失败时可修复后发布新标签；若失败发生在草稿上传中，可重跑同一工作流。已经公开的版本不被工作流覆盖。业务软件版本与面板版本独立，发布面板不会自动更新 Nginx 或数据库。

版本名为 `vX.Y.Z` 或 `vX.Y.Z-rc.1` 等形式。带后缀标签自动标记为 prerelease，默认下载地址只使用正式 Release。正式在此表示 GitHub Release 类型；当前 `v0.1.1` 仍是开发测试版本，生产验收状态以 `acceptance.md` 为准。

每次发布包含：

- `install.sh`：公开 bootstrap，可经 curl 或 wget 下载，支持 `--version`、`--update`。
- `opsview-linux-amd64.tar.gz`：嵌入前端的面板、root 执行器、插件 worker、签名工具、systemd 单元、安装及更新脚本、文档。
- `SHA256SUMS`：发布包及 bootstrap 的 SHA-256 清单。

## 下载与验证流程

1. root、发行版、amd64 和 systemd 预检，获取安装锁，拒绝覆盖已有安装。
2. 未指定版本时，通过 GitHub API 解析最新正式 Release 的准确标签。随后两个资源均使用同一标签 URL，避免安装期间 latest 变化。
3. 使用 HTTPS 下载到私有临时目录；HTTP 错误、网络失败、缺失或重复摘要、校验失败、归档路径或文件类型不合法时停止。
4. 校验通过后解压，执行包内安装或更新程序，退出时清理临时目录。
5. 安装系统基础依赖，检查面板端口，建立专用用户和目录，启动 systemd 服务，检查两个服务及 HTTPS 本机健康接口。
6. 输出实际访问地址及一次性随机账号密码；不会自动更改防火墙和云安全组。远程访问需要放行 TCP 18790。

SHA-256 检查用于验证发布包传输完整性；信任来源仍是 GitHub 仓库、Release 和 HTTPS。未启用第三方镜像或自由下载地址覆盖。

## 无法访问 GitHub 的服务器

在可信任且可访问 GitHub 的设备下载同一 Release 的发布包及 `SHA256SUMS`，将两者上传至目标服务器同一目录，先校验再解压：

```bash
awk '$2 == "opsview-linux-amd64.tar.gz"' SHA256SUMS | sha256sum --check -
tar -xzf opsview-linux-amd64.tar.gz
bash opsview-linux-amd64/deploy/install.sh
# 已有安装使用 deploy/update.sh
```

缺少基础依赖时仍需访问 Ubuntu/Debian 软件源；业务软件安装还需访问其指定源。普通 Docker 容器不具备完整 systemd 环境，脚本会拒绝直接部署。

## 候选与原生配方验证

当前功能以根目录注明的预发布测试版交付，固定版本安装和更新命令见根目录安装教程。GitHub Actions 手动运行，或推送 `codex/software-management` 分支触发 **Build candidate (no production update)**：执行 API 生成、Go race/vet、前端构建、浏览器测试、四系统包测试、面板 systemd 安装回归后上传候选附件；不创建正式 Release、不改 latest、不连接现有服务器。

**Publish verified native packages** 按 `packages/catalog/native-targets.json` 生成矩阵。当前新增目标为 Nginx 的 12 个准确版本 × 四系统，共 48 个组合；每个任务先构建或安装官方包，再在独立干净系统安装二进制。两次都必须通过真实 systemd 启动及协议检查，Nginx 另验证准确版本、可信证书 HTTPS、HTTP/2 和 PCRE 正则重写；旧版私有 PCRE 固定摘要并保留许可证。历史 24 个推荐组合与 rc.7 的 64 个 MySQL/PHP/Redis 组合报告继续保留。只有通过的组合才能进入固定 URL/SHA-256 的受信任目录；常用 PECL 扩展完整矩阵和公网验收仍单独进行。

软件包与面板分别发布，不覆盖既有软件 Release。维护者先推送与验收提交一致的 `software-<run-id>` 标签；若集成令牌创建 Release 时返回 403，可推送 `software-publish-<run-id>` 触发 **Publish accepted software artifacts**，它按当前声明矩阵核对原始工作流各任务的最新结果、全部报告、文件摘要及原始软件标签后复用产物发布。首批已发布为 `software-44057101979`，原始报告随仓库保存。

全部功能和矩阵未验收前只作为开发候选或预发布测试版；不得将带待实现项的构建标记为完整首版。发布工作流额外执行真实 Nginx 轮转和 PHP Session 协议 smoke，发布后在临时 Ubuntu 24.04 主机从公开固定标签下载、安装、登录、改端口并运行 ov 更新。

各功能的实现边界见根目录 [软件管理实现说明](../软件管理实现说明.md)。


## v0.1.3-rc.4 导航验收

本版发布工作流继续使用最终 Linux 发布二进制运行完整 Playwright；新增已缓存菜单切换 P95 ≤300ms、慢接口导航反馈 ≤200ms、后台暂停、受保护路径历史过滤和路由代码加载失败后的重试验收。性能计时在浏览器内完成，不将 Playwright 点击等待计入页面耗时。公网首次加载不适用该缓存切换指标。


对已诊断为网络等临时故障的失败任务，可推送 `software-retry-<run-id>-2` 标签触发 **Retry transient native acceptance failures**。标签指向包含该工作流的维护提交；工作流核对原始运行、仓库、原始 `software-<run-id>` 标签和下一次尝试编号，只重跑失败及其依赖任务。仍失败时先诊断，最多再用 `-3`，不覆盖既有标签。重试不跳过安装检查；发布要求当前矩阵每个组合均成功，下载产物后再次核对报告及摘要。
