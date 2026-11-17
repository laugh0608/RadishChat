# 独立运行 `ping` → `pong` Bot

此示例使用 Go 标准库，按公开 API 读取群成员主动发出的 `ping`，然后回复 `pong`。每个进程对应一个实例内的一次群安装。需要运行支持 [BOT-02 事件与回复](../../docs/topics/bots.md#bot-02) 的 RadishChat 实例。

## 创建、安装和配置

1. Bot 所有者在目标实例的「管理 Bot」中新建 Bot，复制 Bot ID，签发凭据。将凭据明文保存到进程可读、权限为 `0600` 的文件。签发页面只展示一次明文。
2. 群主在目标群的「群 Bot」中填写 Bot ID，授予 `installation:read`、`commands:receive` 和 `commands:reply`。跨账号安装由 Bot 所有者在「管理 Bot」中批准。普通成员可查看已生效安装的权限。
3. Bot 所有者在已登录目标实例的浏览器中，用同源 `GET /api/v1/bots/{bot}/installations` 查询 `state` 为 `active` 的记录，取目标群的 `roomId` 和 `installationId`。可在当前实例的浏览器开发者工具中执行以下语句，替换 Bot ID。 [Web 管理操作](../../docs/topics/bots.md#bot-01-web) · [安装接口](../../docs/api/v1.md#bot-installations)

   ```js
   const botId = '替换为 Bot ID';
   const result = await fetch(`/api/v1/bots/${encodeURIComponent(botId)}/installations`);
   if (!result.ok) throw new Error(`HTTP ${result.status}`);
   console.table((await result.json()).filter(row => row.state === 'active'));
   ```

   同一个 Bot 安装在多个群时，群主可用 `GET /api/v1/rooms` 查目标群的 `name` 和 `id`，将该 `id` 交给 Bot 所有者筛选安装记录。 [会话接口](../../docs/api/v1.md)

从仓库根目录构建并启动。将示例 URL、群 ID、安装 ID 和凭据路径替换为实际值。使用长期保留且仅由运行账号访问的游标目录，同一实例和安装只启动一个进程。

```sh
umask 077
mkdir -p bin
go build -mod=readonly -o ./bin/pingbot ./examples/pingbot
RADISHCHAT_BOT_TOKEN_FILE="$HOME/.config/radishchat/pingbot.token" \
  ./bin/pingbot -instance https://chat.example.com -room 42 \
  -installation '替换为 installationId' \
  -state-dir "$HOME/.local/state/radishchat/pingbot"
```

运行环境也可用 `RADISHCHAT_BOT_TOKEN` 注入凭据，两种凭据来源任选其一。远程实例使用 HTTPS，私有 CA 用 `-ca-file /path/to/ca.pem` 添加到信任链。本机隔离测试接受回环 HTTP。群成员可从「群 Bot」面板发出 `ping`，群历史应出现一条带 Bot 标识的 `pong`。

## 重启、重试和恢复

- 用 `Ctrl+C` 停止。以原实例来源、安装 ID 和游标目录重启，继续上次已确认的位置。游标按实例来源与安装 ID 分文件保存。每页全部回复收到 `201` 或同文重试收到 `200` 后才写入游标。进程中断可能重读该页，服务端按事件去重。
- 网络失败和 `5xx` 用带抖动的有上限退避重试。`429` 至少等待 `Retry-After` 指定的时长。当前页读完后等待 5 秒，有下一页时立即继续。
- 回复收到 `410` 时从最后保存的游标重新拉取。拉取返回 `410` 时，示例显示可用的 `resumeAfter` 并退出。确认接受该缺口后，用原启动参数加 `-resume-after '<日志中的值>'` 重启。仅当服务端仍报告同一边界才跳过缺口。若边界已有变化，检查新的提示并重新确认。确认完成后从启动配置移除该参数。 [事件接口](../../docs/api/v1.md#bot-command-events) · [回复接口](../../docs/api/v1.md#bot-command-replies)
- `401` 时进程退出。轮换凭据后，用新凭据和原安装 ID、游标目录重启。`404` 时进程退出，请核对群、安装和授权。重新安装或恢复数据库时，使用新安装 ID 启动，该安装从独立游标开始。恢复后还须重新签发凭据。 [备份与恢复](../../deploy/README.md#sqlite-backup-restore)
