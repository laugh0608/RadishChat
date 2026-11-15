# P1 公网邀请码准入验收

2026-11-15，[AUTH-01-02](../topics/identity.md#auth-01-02) 通过单项验收。使用 `705023e` 及本轮 AUTH-01-02 改动。在 macOS arm64 用 Go 1.26.3、Node.js 24 和本机构建的 Web 产物，使用隔离 SQLite 与合成账号验证。

| 检查 | 实际结果 |
| --- | --- |
| 注册与会话 | `invite` 下缺失或错误的邀请码返回 `403`，且账号和会话数均为零。有效码返回 `201`，当前会话属于申请的账号。重复用户名返回 `409`，账号和会话数量保持一致。 |
| 配置、轮换与关闭 | Go 启动时拒绝缺少邀请码的 `invite` 配置，并在创建数据库前退出。Compose 配置能传递 `invite` 模式和合成码。更换邀请码并重启后旧码被拒绝。切换 `off` 后当前有效码也被拒绝，已有账号仍能登录。 |
| Web 路径 | 用 Playwright 驱动 Chromium 访问回环实例。登录表单没有邀请码字段，邀请码注册表单必填。空值没有发出注册请求，错误值返回 `403` 并显示提示，有效值返回 `201` 后进入会话页。切换为 `off` 并刷新后，注册入口隐藏，原账号仍可登录。 |
| 外部身份 | 在合成发行方测试中，邀请码模式保留已关联 Radish 账号的登录，拒绝给新的外部身份建号。 |
| 工程检查 | `go test -mod=readonly -count=1 -race ./...`、`go vet -mod=readonly ./...`、Go 构建、`npm ci --offline`、`npm run build`、`python3 scripts/check-repo.py` 和 `git diff --check` 均通过。 |

本次浏览器实测使用回环 HTTP。候选产物与目标 HTTPS 部署由 [OPS-01-03](../topics/deployment.md#ops-01-03) 联合验收。邀请码由部署配置提供，SQLite 结构沿用现有版本。[配置说明](../topics/identity.md#local-admission-config)
