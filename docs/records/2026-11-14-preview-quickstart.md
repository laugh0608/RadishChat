# 探索预览的启动说明

2026-11-14。本文保存探索版本的操作说明，正式路径以[整体规划](../planning/project-plan.md)为准。

从当前 P1 源码启动时，注册默认关闭。参见[本地准入与 HTTPS 反代配置](../topics/identity.md#local-admission-config)。

Radish 系列的独立即时通讯项目。核心就是**私聊、群组、Markdown 消息**。自部署实例可用本地账号，也可选接入 Radish 账号。各实例独立管理成员和聊天历史。

当前交付为 **Go + React 的单实例开发预览**。浏览器可切换到任一部署了同版 Web 的实例。Flutter 安装包、Bot、端到端加密和消息自动消失尚未交付。这里的「私聊」表示只有会话成员能通过 API 读取，实例运营方仍可读取存储中的正文。

## 从源码启动

需要 Go 1.26、Node.js 24 和 npm：

```sh
npm --prefix web ci
npm --prefix web run build
go run ./cmd/radishchat
```

打开 [127.0.0.1:8080](http://127.0.0.1:8080)，分别注册两个账号，用对方完整用户名发起私聊或把对方加到群组。两次登录可用普通窗口和隐私窗口。默认数据库为 `data/chat.sqlite`。

## 使用 Docker Compose 自部署

```sh
docker compose -f deploy/compose.yaml up --build -d
```

默认只在主机的回环地址开放 8080，数据保存在 Compose 的具名卷 `chat-data`。远程使用时设置 `RADISHCHAT_PUBLIC_URL=https://你的实例域名`，并在主机用 HTTPS 反向代理（示例：[Caddyfile](../../deploy/Caddyfile.example)）。每个生产实例用不同的主机名。反代应支持事件流，不缓存 API，也不要记录 OIDC 回调的查询参数。停服后备份 SQLite 文件或数据卷；不要使用 `docker compose down -v` 做常规升级。

| 设置 | 默认值 / 用途 |
| --- | --- |
| `RADISHCHAT_PUBLIC_URL` | `http://127.0.0.1:8080`；用户浏览器访问的源地址（不带路径），应与 TLS 反代域名和端口完全一致 |
| `RADISHCHAT_LISTEN` | 源码启动为 `127.0.0.1:8080`；Docker 内为 `0.0.0.0:8080` |
| `RADISHCHAT_PORT` | Compose 的主机回环端口，默认 8080；本地 HTTP 改端口时同步改公开 URL |
| `RADISHCHAT_DATABASE` | 源码启动为 `data/chat.sqlite`；Docker 内为 `/data/chat.sqlite` |
| `RADISHCHAT_WEB_DIR` | 源码启动为 `web/dist`；Docker 内为 `/app/web` |
| `RADISHCHAT_REGISTRATION` | `on` 接受本地注册和首次 Radish 登录建号；`off` 只允许已有账号登录 |

这是单实例应用。SQLite 卷不供多个副本共享。受邀用户可以直接被群主拉入群，新成员能看到聊天历史；被移出或退出后不能再读取。当前适合熟人或小团队测试，无密码找回、公网陌生人防骚扰或安装包发行。

## 可选：Radish 登录

给本实例注册独立的机密客户端，允许的回调必须是 `<RADISHCHAT_PUBLIC_URL>/api/v1/auth/radish/callback`。配置 `RADISHCHAT_OIDC_ISSUER`、`RADISHCHAT_OIDC_CLIENT_ID` 和 `RADISHCHAT_OIDC_CLIENT_SECRET`；直接运行时也支持 `RADISHCHAT_OIDC_CLIENT_SECRET_FILE` 作为替代。使用 `client_secret_post` 时设置 `RADISHCHAT_OIDC_CLIENT_AUTH=client_secret_post`（默认 `client_secret_basic`）。不要把密钥放进前端、镜像或源码。

本地注册与 Radish 登录并存。首次 Radish 登录按发行方和稳定用户标识创建本实例账号；不会自动与同名本地账号合并。自部署无需 Radish 账号或请求官方服务。实例之间不会自动共享历史。真实 Radish 的客户端注册和部署联调需另行完成。

- [当前状态与路线图](../status/current.md)
- [产品和实例边界](../adr/0001-product-boundary.md)
- [API v1](../api/v1.md)

本地检查：`go test ./...`、`go vet ./...`、`npm --prefix web run build`。许可待项目所有者决定。
