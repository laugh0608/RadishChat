# P1 默认准入与本地会话验收

2026-11-15，[AUTH-01-01](../topics/identity.md#auth-01-01) 通过单项验收。源码以 `dev` 上的 `0acfda8` 为基线，叠加本轮 AUTH-01-01 改动。用 Go 1.26.3 编译，在 macOS 27.0.1 arm64 和 Linux arm64 容器运行。测试使用空 SQLite 库、合成账号及回环或 Docker 隔离网络。

| 检查 | 实际结果 |
| --- | --- |
| 启动与准入 | Go 进程缺省配置返回 `registration=false`，注册返回 `403` 且未建立会话。设为 `on` 后可创建本地账号，恢复 `off` 后拒绝新注册，已建账号仍可登录。`docker compose config` 解析出的注册默认值为 `off`。 |
| HTTPS 会话与同源 | 通过临时 Caddy 2.11.4 HTTPS 反代验证 `__Host-` Cookie 的 `Secure`、`HttpOnly`、`SameSite=Lax`、根路径、七天有效期和站点域范围。重复登录和退出后，原 Cookie 请求 `/api/v1/me` 返回 `401`。跨源写请求返回 `403`。Go 回归测试另覆盖缺少 `Origin` 的写请求，以及把会话设为到期后 API 返回 `401`。 |
| 登录限流 | 两个具有不同客户端 IP 的容器经同一个 Caddy 访问。首个来源耗尽连接来源额度并收到 `429`，第二个来源登录成功。Go 回归测试覆盖直连来源、可信反代、无头与多值回退，以及 IPv4 映射地址归一化。账号额度继续独立计算。 |
| 工程检查 | `go mod verify`、`go test -mod=readonly -count=1 -race ./...`、`go vet -mod=readonly ./...`、Linux arm64 Go 构建、`python3 scripts/check-repo.py` 与 `git diff --check` 均通过。Go 套件包含合成 OIDC 回归。 |

HTTPS 实测用本轮 Go 二进制、本地签发证书和两个临时客户端。以同一个临时数据卷先后验证注册缺省、开启和关闭，再分别发送请求核对两个来源的限流行为。容器、网络和数据卷均已清理。[配置说明](../topics/identity.md#local-admission-config)

公网邀请码由 [AUTH-01-02](../topics/identity.md#auth-01-02) 接续。完整候选镜像与目标 HTTPS 部署、备份恢复后的会话失效分别由 [OPS-01-03](../topics/deployment.md#ops-01-03) 和 [OPS-01-04](../topics/deployment.md#ops-01-04) 验收。真实 Radish 登录按 [AUTH-02](../topics/identity.md#auth-02) 单独安排。
