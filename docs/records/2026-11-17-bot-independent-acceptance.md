# BOT-02-03 示例 Bot 与独立接入验收

2026-11-17，使用 `dev` 基线 `3c44e161adfa9f5308f9808fb1dbd769e1930347` 的服务端和本轮 Go 示例。第二个客户端用 Python 3.9 标准库按公开 API 实现，每次运行处理一页，游标文件由操作者按实例和安装分配。其验收源码为 [independent_bot.py](../../testdata/bot-acceptance/independent_bot.py)，SHA-256 为 `eafaeacfd15d64a2f0d42fe59331243475c82f6d07923b290b238a0fffe2b3eb`。[运行说明](../../examples/pingbot/README.md) · [实施条目](../topics/bots.md#bot-02-03)

本机为 macOS 27.0.1 arm64，使用 Go 1.26.3。两套 HTTPS 实例仅绑定回环地址，各自使用 SQLite v7 数据库和合成账号。两个运行客户端均校验证书信任和主机名。Web 另在回环 HTTP 用 Playwright CLI 0.1.22 驱动 Chromium 复验，静态文件取自已验收的 `radishchat:p2-bot-reply-8dd3503` 镜像，配合同一服务端和实际 Go 示例进程。[Web 候选来源](2026-11-15-bot-command-replies.md)

## 复跑步骤

1. 从仓库根目录用 `go build -mod=readonly -o "$acceptance_dir/" ./cmd/radishchat ./examples/pingbot` 构建两个二进制，`acceptance_dir` 为已创建的受保护临时目录。启动两个各自托管 SQLite 的实例，为两套 HTTPS 反向代理配置可由客户端校验的证书并设置对应的 `RADISHCHAT_PUBLIC_URL`。每个实例用不同账号担任群主、Bot 所有者和普通群成员。
2. 按运行说明创建 Bot、签发凭据及安装。群主授予三项约定权限，Bot 所有者批准跨账号请求。在所有者的安装列表读取 `roomId` 与 `installationId`，用普通成员发 `ping`，再对照群历史。交换两实例的 Bot 凭据和安装 ID 验证拒绝结果。
3. 用运行说明中的 Go 示例连接 A。用 Python 独立客户端连接 B，传入 B 的实例来源、群、安装和凭据。每次运行处理一页，保留同一游标文件以验证续读，遇到 `hasMore=True` 时可再次运行。换实例或重装时分配新文件。以下变量分别指向 B 的来源 URL、群 ID、安装 ID、私有凭据文件、该次安装专用游标文件和信任的 CA。

   ```sh
   python3 testdata/bot-acceptance/independent_bot.py \
     --instance "$bot_origin" --room "$bot_room" --installation "$bot_installation" \
     --token-file "$bot_token_file" --cursor-file "$bot_cursor_file" --ca-file "$acceptance_ca"
   ```

4. 在回环代理临时注入断线、`503`、短 `Retry-After` 和回复提交后丢失响应。半页确认时终止 Go，核对游标文件，重启后对照回复状态与历史数量。用该安装已有的两条待处理事件验证过期，在回复第一条前将其临时数据库中的 `occurred_at` 置到保留窗口以外，保留第二条。核对未经确认的缺口、用旧值确认及用当前 `resumeAfter` 确认的三种结果。另用 26 条事件跨默认分页边界，并用正常 API 调用触发服务端拉取限流。
5. 轮换凭据，向旧凭据发起拉取，移除安装，再使用新安装 ID 复跑。停 A，按部署说明用 CLI 备份并恢复到新 SQLite 路径，在原 HTTPS 来源重新启动。核对旧会话、凭据和安装状态，再签发凭据、批准新安装，用 Python 客户端在 A 回复一条新命令。同期核对 B 历史和拉取仍可用。[SQLite 备份与恢复](../../deploy/README.md#sqlite-backup-restore)

## 实际结果

| 检查 | 结果 |
| --- | --- |
| Web 端到端 | 群主在「群 Bot」预览并显式授予接收、回复权限。390 × 844 视口的普通成员使用「发送 ping」，收到入队提示，随后群历史实时出现独立 Go 进程发出的 `pong`、展示名和 `Bot` 标识。 |
| 双实例和权限 | 跨账号审批后开始投递。两实例互换凭据返回 `401`，使用另一实例的安装 ID 返回 `404`。普通聊天正文未进入待交付事件。Bot 凭据不能读取真人消息接口，Bot 所有者身份也不能读取未加入群的历史。 |
| 游标、重试与去重 | 注入断线和 `503` 后 Go 自动重试。杀停只完成一条回复的两事件页时未写入游标，重启后的第一条同文重试返回 `200`。第二条回复提交后的响应被丢弃，再次提交返回 `200`，群历史累计两条回复。保存后重启从原游标继续，26 条新事件分两页处理。共用游标目录时两实例和新安装使用各自的文件。 |
| 限流与缺口 | 注入 `Retry-After: 2` 后，实测下一次请求至少等待约 2 秒。真实拉取限流返回 `429` 和 `Retry-After: 60`，Go 进入 60 秒等待并能响应停止信号。回复过期得到 `410`，从原游标重拉得到缺口，停在原游标。旧确认值被拒绝，确认当前边界后处理仍在保留期的命令。 |
| 凭据与安装撤销 | Go 运行时遇到轮换返回的 `401` 和移除后的 `404` 均退出。Python 在 B 验证了丢失回复后重跑返回 `200`、持久化续读、轮换前后凭据状态、移除后 `404`，以及新安装首次回复 `201`。 |
| 备份恢复与实例隔离 | A 恢复后旧会话和凭据返回 `401`。重新签发后旧安装返回 `404`，新安装没有旧的待交付命令。原真人消息和五条 Bot 回复保留，Python 用新凭据和新安装处理新命令。B 的历史和安装继续可用。 |
| 工程检查 | Go 示例与服务端构建、`go test -mod=readonly -count=1 -race ./...`、`go vet -mod=readonly ./...`、`python3 scripts/check-repo.py` 和 `git diff --check` 通过。新增 Go 回归测试覆盖半页确认和缺口确认。 |

本轮使用合成数据在本机验收。浏览器会话、临时进程、证书、凭据和数据库均已清理。
