# GitHub 发布与一键安装

仓库：`gzdzh-cn/dzh-OpsView`，公开访问。部署机器从 GitHub Releases 下载编译好的 Linux amd64 包；不拉取源码编译，不需要 GitHub Token。

## 首次发布及后续版本

维护者在代码验证完成后推送版本标签：

```bash
make check
./scripts/test-installer.sh
git push github main
git tag -a v0.1.0 -m 'OpsView 0.1.0 development release'
git push github v0.1.0
```

GitHub Actions 的 `Publish Linux release` 工作流会执行 Go race 测试与 vet、前端测试与构建、安装器检查和 Playwright，再构建四个 Linux 二进制。构建 Job 只有读权限，发布 Job 使用仓库自动提供的 `GITHUB_TOKEN` 写 Release，无需额外保存个人 Token。需要仓库启用 Actions，且组织策略允许工作流写 Release。

构建成功后才创建草稿、上传完整产物、发布 Release。工作流失败时可修复后发布新标签；若失败发生在草稿上传中，可重跑同一工作流。已经公开的版本不被工作流覆盖。业务软件版本与面板版本独立，发布面板不会自动更新 Nginx 或数据库。

版本名为 `vX.Y.Z` 或 `vX.Y.Z-rc.1` 等形式。带后缀标签自动标记为 prerelease，默认下载地址只使用正式 Release。正式在此表示 GitHub Release 类型；当前 `v0.1.0` 仍是开发测试版本，生产验收状态以 `acceptance.md` 为准。

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
6. 输出访问地址和初始化令牌；不会自动更改防火墙和云安全组。远程访问需要放行 TCP 18790。

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
