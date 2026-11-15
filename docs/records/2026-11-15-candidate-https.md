# P1 候选镜像与 HTTPS 验收

2026-11-15，[OPS-01-03](../topics/deployment.md#ops-01-03) 本机验收通过。使用 macOS 27.0.1 arm64、OrbStack Docker 29.8.2、Compose 5.5.0 和 Caddy 2.11.4。从只包含部署配置的干净目录启动，使用合成账号和新建命名卷。

## 候选产物

- 构建命令为 `docker build --progress=plain -t radishchat:p1-ops-01-03-20261115 .`。通过仓库 Dockerfile 完成 Web、Go 和 Alpine 运行层构建。候选构建复用同次验证已通过的 Web 和 Go 构建层。
- 应用源码基线为 `95b3f768c6b86832f27b3679c2a2abd1b06d276e`。影响镜像的工作区变更为 Dockerfile，其 SHA-256 为 `dbadd556d9d088a1a7135275bc5ffcd47c8c7ac111675180e25cf4ddfe05df2e`。
- 本机标签为 `radishchat:p1-ops-01-03-20261115`，平台 `linux/arm64`，本地镜像 ID 为 `sha256:c7fda1f90b08891a29feb9e4d24fbdf582aa96dbf922772d7e4f96009294ad58`。

## 实测结果

| 检查 | 实际结果 |
| --- | --- |
| 安装和 HTTPS | 单实例 `--no-build --wait` 从干净目录启动，默认关闭注册与 Radish 登录。双实例均达到 `healthy`，经 Caddy 访问 `chat-a.radishchat.test:8443` 和 `chat-b.radishchat.test:8443` 均通过 CA 链与主机名校验，`/healthz` 返回 200。两个应用无宿主机端口映射，只有 Caddy 映射本机回环。 |
| 准入、收发和补拉 | 两个入口缺少邀请码时均返回 403。Playwright 驱动的 Chromium 分别在两个入口注册普通账号、发起私聊并双向收发。会话 Cookie 为 `__Host-radishchat_session`，具有 `Secure`、`HttpOnly`、`SameSite=Lax`。A 的接收端断线后，经 HTTPS 写入 105 条消息，重连恢复 SSE，按序补齐全部消息且无重复，并收到下一条实时消息。 |
| 数据读回 | 强制重建 A、B 应用容器，核对容器 ID 变化且命名卷保留。用新浏览器会话分别登录，在两个实例读回各自的私聊消息。 |
| 卷与日志 | A、B 使用独立卷，应用以 UID `10001` 运行，卷目录和 SQLite 文件权限分别为 `0700`、`0600`。使用另一非特权 UID 核对目录不可进入、数据库不可读取。检查重建前后的应用和 Caddy 日志，未发现合成密码、邀请码、会话值或测试消息正文。 |

证书链和主机名由 `curl --cacert --resolve` 验证。Chromium 使用进程内域名映射，并限定信任到测试站点的证书公钥，系统信任配置保持原样。仓库检查和差异空白检查均通过。验收后清理了临时容器、网络、卷和浏览器状态，保留本地候选镜像。

复跑步骤见[部署说明](../../deploy/README.md)。公网 DNS、公有证书和目标主机的实际反代路径留到目标环境验证。独立实例切换和备份恢复分别按 [WEB-01-03](../topics/clients.md#web-01-03) 与 [OPS-01-04](../topics/deployment.md#ops-01-04) 接续。
