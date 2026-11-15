# P1 消息确认与 Markdown 验收

2026-11-15，[MSG-02-01](../topics/messaging.md#msg-02-01) 和 [MSG-03-01](../topics/messaging.md#msg-03-01) 通过单项验收。以 `d4d04be` 为源码基线，叠加本轮改动。在 macOS 27.0.1 arm64 上用 Go 1.26.3、Node.js 24.16.0 构建。测试使用回环 HTTP、临时 SQLite 和合成账号。

| 检查 | 实际结果 |
| --- | --- |
| 消息确认、重试与权限 | Go 测试在实际处理器提交后断开 HTTP 连接，客户端收到 `EOF`，收方历史已有一条消息。原 `requestId` 和原文重试返回 `200` 和同一条记录，改正文返回 `409`。重放注销的 Cookie 返回 `401`，重新登录后被移出群再重试返回 `404`。历史始终只有原记录。另用 SQLite 触发器阻断写入，首次返回 `500` 且历史为空，解除阻断后原标识可成功发送。 |
| Chromium 重试 | 浏览器测试丢弃服务器首次 `201` 响应。编辑器保留原文，第二次发送沿用同一 `requestId`，返回 `200` 和原消息；收方只看到一条。 |
| 正文与码点上限 | Go 接受含列表、代码块、emoji 和首尾换行的 8,000 码点原文，发送响应与收方历史均逐字一致。8,001 码点返回 `400`。Chromium 实际发送 8,000 个 emoji，响应原文完整。多一个 emoji 时编辑器保留草稿、提示超限并禁用发送。 |
| Markdown 渲染 | 桌面预览隐藏编辑框。窄屏收方与发送前预览生成相同的正文 DOM，覆盖有序和无序列表、GFM 表格和任务项、代码块。原始 HTML 被过滤，图片显示文字占位。仅绝对 HTTP(S) 链接可点击，使用新标签页并加 `noreferrer noopener`。监听渲染时的网络请求，未观察到外部资源请求。 |
| 工程检查 | Go 构建、`go test -mod=readonly -count=1 -race ./...`、`go vet -mod=readonly ./...`、Web 离线依赖安装与生产构建、`python3 scripts/check-repo.py` 和 `git diff --check` 均通过。 |

多页历史与离线补拉由 [MSG-02-02](../topics/messaging.md#msg-02-02) 接续。浏览器连续断网与撤权后的工作区恢复由 [WEB-01-02](../topics/clients.md#web-01-02) 联合验收。
