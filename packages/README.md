# 软件目录及独立插件规范

`catalog/versions.json` 是软件目录；`plugins/index.json` 是内置市场。镜像必须以 `@sha256:<64 位十六进制>` 固定，原生下载使用 HTTPS 和 SHA-256。JSON 同时嵌入后端的 internal/catalog，修改内置目录时须同步两处并测试。

## 软件清单

参考 `examples/software.json`。必填 id、software、version、platforms；source/sha256 或 image 至少一组。software 支持 nginx、mysql、postgresql、nodejs、php、redis。版本 ID 不可覆盖已有条目，更新必须创建新 ID。platforms 必须包含 linux/amd64。

- Nginx、PostgreSQL：匹配安装器的官方源代码归档，目录结构按首层剥离。
- MySQL：匹配 Linux amd64 的官方二进制 tar 归档，版本依赖由发布者预先验证。
- Node：Linux x64 二进制归档。
- 自定义来源复用现有安装流程，不支持下载后执行任意 root 脚本。

## 签名封装

外部软件目录和插件使用 Ed25519，封装字段如下：

```json
{"keyId":"team","payload":"BASE64_OF_EXACT_JSON_BYTES","signature":"BASE64_ED25519_SIGNATURE"}
```

签名覆盖解码后的 payload 原始字节，不对 JSON 重新排序。公钥为 Base64 编码的 32 字节 Ed25519 公钥，写入 `/etc/opsview/trust.d/team.pub`，由 root 信任。

```bash
./dist/local/opsview-pack -mode keygen -key /tmp/team.key
./dist/local/opsview-pack -mode sign -key /tmp/team.key -id team -input packages/examples/software.json -output /tmp/software.signed.json
# 以下在 Linux 目标主机执行，上传的是公钥：
sudo opsview trust-key --id team --file /path/to/team.key.pub
```

私钥由仓库维护者离线保管。示例含占位摘要，签名成功不代表工件验证成功，须替换为实际可下载内容的摘要。面板导入先校验签名，再校验平台、来源与安装器，安装时校验实际内容。

## 插件清单

参考 `examples/operations-plugin.json` 和内置插件清单。字段包含 id、name、version、type、description、minPanelVersion、platforms、permissions、dependencies、form；第三方 builtin 必须为 false。面板 0.1.1 继续接受 minPanelVersion 为 0.1.0 的现有清单。

| 类型 | 内容 |
| --- | --- |
| application | 固定摘要的 Compose 模板，必须声明 docker.compose，允许 docker 依赖 |
| operations/backup | workerUrl + sha256，独立低权限进程，JSON-RPC health/check |

表单支持 text、password、port，字段有 key、label、default。应用模板使用 `{{key}}` 插值，所有表单值拒绝换行、Shell/Compose 插值和结构控制字符。Compose 验证拒绝 privileged、host network、Docker Socket、宿主机目录和外部卷。数据卷属于项目，卸载默认保留。

第三方前端 JavaScript 不加载。声明式表单由内置页面渲染。升级在市场“配置 / 升级”重新验证清单、权限并保留配置及卷；失败保留旧配置并尝试恢复 Compose。内置条目随面板更新，外部仓库按同步后的签名清单升级。

## 静态仓库

通过 HTTPS 发布 `{"plugins":[SIGNED_ENVELOPE, ...]}`。最多 100 条，每条独立签名。仓库可锁定 keyId；同步失败不替换已有列表。禁止覆盖内置插件标识。默认市场是随发布包内置的目录，不依赖宝塔市场。

## Worker 协议 1.0

一次调用通过 stdin 输入一行 JSON，通过 stdout 输出一行 JSON-RPC 2.0；不把日志混入 stdout。首版允许 `health` 和 `check` 两个方法，响应时间最多 30 秒。

```json
{"jsonrpc":"2.0","id":1,"method":"health","params":{"protocol":"1.0","permissions":[],"config":{}}}
```

```json
{"jsonrpc":"2.0","id":1,"result":{"status":"ok","protocol":"1.0"}}
```

worker 通过 systemd-run 以插件专用用户启动，NoNewPrivileges、只读系统、独立临时目录、无设备及能力，128 MiB 内存、32 个任务，只写自身 data 目录。没有 network.outbound 时限制为 AF_UNIX；此权限是地址族约束，并非细粒度域名白名单。Docker Socket 和特权动作不提供给 worker。

内置 S3 worker 的 check 使用表单配置执行 HeadBucket；实际备份通过执行器内的固定 S3 适配器完成。外部 worker 的 check 可实现自己的检查逻辑，不提供任意 root 命令回调。

## 原生预编译产物

条目可声明 `recipeRevision`、`methods`（`source`/`binary`）、`recommended`、`disabledReason` 和 `binaries`。`disabledReason` 非空时执行器拒绝安装。预编译包按 `system` 精确匹配 `ubuntu:22.04`、`ubuntu:24.04`、`debian:12`、`debian:13`，每个产物还需 HTTPS `source`、SHA-256 和以 `/var/lib/opsview/` 开头、以 `/runtime` 结尾的 `buildPrefix`。

构建使用 **Validate native packages** 工作流，产物附 JSON 验证记录。产物未进行签名、系统验证及清单导入之前，不进入默认可选目录；不得把验证附件当作已发布包。导入时使用新的条目 ID，禁止覆盖原条目；安装器校验摘要，初始化独立实例，PHP helper 脚本仅替换受控构建前缀。`conf/`、密码和业务数据不应打包到运行时归档。

源码编译和扩展构建使用 `ovbuild`，安装目录由 root 管理。首批常用 PHP 内置扩展与 5 种固定版本 PECL 配方见 `server/internal/catalog/extensions.json`；旧 PHP 隔离依赖配方尚未验证，目录明确禁用。

新增带 `recipeRevision` 的原生配方需要 `verifiedSystems`，例如 `{"source":["ubuntu:24.04"]}`。只有完成该系统实际原生服务验收后才登记；源码编译 smoke 报告中的 `systemdVerified: false` 不能作为提升依据。`availability` 是 API 按当前系统计算的只读原因，浏览器不能用它绕过执行器校验。
