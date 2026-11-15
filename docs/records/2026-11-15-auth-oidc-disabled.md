# P1 外部登录禁用验收

2026-11-15，[AUTH-02-01](../topics/identity.md#auth-02-01) 通过单项验收。使用 `0b2117e` 的 Go 和 Web 运行代码及本轮回归用例。在 macOS 27.0.1 arm64、Go 1.26.3、Node.js 24.16.0 上构建，以回环 HTTP 和临时 SQLite 运行。

| 检查 | 实际结果 |
| --- | --- |
| 缺省配置与 Web 入口 | Compose 缺省配置将 OIDC 环境变量留空，注册为 `off`。先对临时库显式开放注册以创建两个合成账号，再省略注册和 OIDC 配置重启 Go 进程。`GET /api/v1/info` 返回 `radishLogin=false` 和 `registration=false`，Chromium 登录页仅显示本地登录。 |
| OIDC 端点 | 同源 `POST /api/v1/auth/radish/start` 及带合成授权码和状态的 `GET /api/v1/auth/radish/callback` 均返回 `404`。响应没有跳转或设置 Cookie，Go 回归确认未写入授权流程和会话。 |
| 本地聊天 | 两个合成账号先后在 Chromium 登录。Alice 创建与 Bob 的私聊并发送 Markdown 消息，Bob 登录后从会话历史读到同一条消息。 |
| 工程检查 | Go 构建、`go test -mod=readonly -count=1 -race ./...`、`go vet -mod=readonly ./...`、Web 离线依赖安装与生产构建、`python3 scripts/check-repo.py`、`git diff --check` 均通过。Go 全量测试包含启用 OIDC 的合成发行方用例。 |

本次验收覆盖 P1 缺省禁用路径。真实 Radish 发行方的接入仍按 [AUTH-02](../topics/identity.md#auth-02) 单独安排，候选镜像和目标 HTTPS 部署由 [OPS-01-03](../topics/deployment.md#ops-01-03) 验收。
