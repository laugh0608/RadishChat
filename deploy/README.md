# 候选镜像和 HTTPS 验收

在仓库根目录用 Docker 和 Docker Compose V2 构建候选镜像。记录源码提交、工作区差异、镜像 ID 和平台。发布与跨机器验收时应记录分发镜像的摘要。

```sh
docker build --tag radishchat:p1-candidate .
docker image inspect --format '{{.Id}} {{.Os}}/{{.Architecture}}' radishchat:p1-candidate
```

## 单实例

将 `compose.yaml` 和 `Caddyfile.example` 复制到安装目录。按实际域名修改 Caddy 配置，在安装目录创建权限为 `0600` 的环境文件，并设置 `RADISHCHAT_IMAGE` 为本机已有的候选标签，`RADISHCHAT_PUBLIC_URL` 为浏览器访问的完整 HTTPS 来源（含非默认端口时也要填端口）。从该目录运行 `docker compose --env-file instance.env up -d --no-build --wait`。

- 应用默认只在宿主机回环地址发布端口。Caddy 示例在同一宿主机接收 HTTPS，再反代到 `127.0.0.1:8080`。如果设置 `RADISHCHAT_PORT`，同步修改 Caddy 的上游端口。公有证书的申请和续期要求部署者配置域名及 Caddy 所需的入站访问。
- 默认关闭本地注册。内测启用 `RADISHCHAT_REGISTRATION=invite`，在环境文件中设置由 `openssl rand -hex 24` 生成的 `RADISHCHAT_INVITE_CODE`。内测结束或停止新增账号时将准入设回 `off` 并重建应用容器，已有账号仍可登录。[准入配置](../docs/topics/identity.md#local-admission-config)
- 只在确认应用实际收到的 TCP 对端 IP 是直接反向代理、且代理只传递一个可信客户端地址时，设置 `RADISHCHAT_TRUSTED_PROXY_IP`。宿主机反代经过 Docker 端口转发时，对端可能是 Docker 网关。未设置时，速率限制按应用看到的对端 IP 计数，该代理后的客户端可能共享限额。
- 镜像以 UID `10001` 运行，健康检查访问容器内 `/healthz`。新命名卷的 `/data` 为 `0700`，SQLite 文件为 `0600`。已有卷升级时保留原权限，部署者应检查卷的属主和模式。Docker 管理员可访问命名卷和容器环境变量，应限制 Docker 管理权限。收集诊断资料时保护环境文件、Cookie、邀请码和 SQLite 数据。

<a id="local-account-disable"></a>

## 本地账号停用与恢复

在受控实例的维护环境，核对目标实例和本地账号的数字 `id`。账号本人可从 `/api/v1/me` 核对 ID，群内可用获授权的成员接口核对。使用与运行实例版本一致并拥有数据卷权限的账号执行。Compose 方式从该实例的安装目录运行，按实际操作替换子命令，并把示例中的 `42` 换成目标 ID。

```sh
docker compose --env-file instance.env exec -T chat radishchat account issue-recovery 42
```

直接运行二进制时，使 `RADISHCHAT_DATABASE` 指向目标实例由同版本服务初始化或升级的数据库，再执行相应的 `radishchat account` 命令。服务可以继续运行。版本、账号类型或状态不匹配时命令报错，数据库繁忙时可重试。

- `disable` 撤销账号的旧会话、恢复码及其 Bot 的凭据和安装，保留已有消息和成员关系。重复停用同一账号可安全执行。
- `issue-recovery` 用于可用或待恢复账号。维护者通过已有受控渠道核实本人身份后执行，目标进入待恢复状态且旧会话失效。重签覆盖旧码。到期、遗失或错过输出时可重签。
- `reopen` 用于已经停用的账号。维护者确认解除停用并核实本人后执行，账号进入待恢复状态。本人成功重置密码后才可登录。恢复后的 Bot 需要重新签发凭据和安装。

签发或解除停用后的输出是一枚 15 分钟有效的一次性恢复码。在受保护终端读取，通过已核实的渠道交给账号本人，运维记录排除恢复码。本人在目标实例登录页选择「忘记密码？」，填写用户名、恢复码和新密码，再用新密码登录。若提交后连接中断，先尝试用新密码登录，再按需联系维护者重签。[账号恢复与停用合同](../docs/topics/identity.md#auth-03-local-design)

<a id="maintenance-audit"></a>

## 本机维护审计

SQLite v9 起，停用、签发或重签恢复码、解除停用与维护审计同一事务提交。写入审计失败时可按命令错误处理并重试，已有状态、会话和 Bot 授权保留。成功重复停用记录为 `already_disabled`，便于区分首次停用。

使用具备实例数据卷访问权限的维护身份，从该实例安装目录读取最近 90 天的审计。

```sh
docker compose --env-file instance.env exec -T chat radishchat ops audit --limit 50
```

直接运行时使用指向该实例数据库的 `RADISHCHAT_DATABASE` 及 `radishchat ops audit`。命令以只读方式打开现有数据库，按记录编号降序返回 JSON 数组。默认 50 条，`--limit` 可设为 1 至 200。用上一页最后一项的 `id` 指定 `--before` 继续向前查看。字段为 `id`、Unix 毫秒 `occurredAt`、`source`、`action`、`result`，以及适用的 `targetUserId` 和 `reportId`。`source` 表示维护入口，个人归因依赖部署环境的身份记录。到期记录的数据库清理归 `OPS-02-03`。[审计范围](../docs/topics/deployment.md#ops-02-first-batch)

<a id="message-reports"></a>

## 普通群举报审阅与结案

成员在普通群的单条消息下选择「举报」，确认本实例的维护者可以查阅该消息后提交原因，并获得举报编号。部署维护者使用实例的现有维护身份操作。直接运行时使 `RADISHCHAT_DATABASE` 指向目标实例的 v10 数据库。Compose 安装可在同一实例安装目录为以下命令加上 `docker compose --env-file instance.env exec -T chat`。

查看待处理举报。列表按编号降序，仅输出元数据。`--status` 接受 `open` 或 `closed`，省略时查询两类，关闭超过 30 天的举报超出命令查询窗口。`--limit` 默认为 50，范围 1–200。向前翻页使用 `--before` 和上一页最后的编号。

```sh
radishchat ops reports list --status open
```

核对编号后在受保护终端审阅，将 `42` 替换为待查举报。审阅先提交 `report.review` 审计，再输出该举报与所指消息原文。证据已不可用时输出 `evidence: null`。举报原文属于维护输出，交接和诊断材料须保护这部分数据。

```sh
radishchat ops reports review 42
```

完成核实后选择一种结案结果。以下示例用于确认需要停用该条消息的本地账号作者时。维护者以被举报消息的作者为准，命令在同一事务内提交结案、撤权及关联审计。

```sh
radishchat ops reports close 42 account_disabled
```

`dismissed` 用于结束处理且保留当前授权。Bot 举报经维护者和群主沟通后，使用 `referred_to_group_owner` 记录转交，群主按群权限处理安装。以原结果重跑结案可取回原记录，改用不同结果时命令返回错误。到期记录的数据库清理接续于 `OPS-02-03`。[授权与处置设计](../docs/topics/deployment.md#ops-02-first-batch)

<a id="sqlite-backup-restore"></a>

## SQLite 备份、恢复与升级回退

候选镜像中的 `radishchat backup <备份文件>` 从 `RADISHCHAT_DATABASE` 生成快照，`radishchat restore <备份文件>` 向该变量指定的新路径恢复。命令可读取 v1 至 v10 并保留原库版本，检查 SQLite 完整性和外键，清除副本内的登录会话与未完成的 OIDC 流程。v4 起副本保留 Bot 定义及所有者，清除 Bot 凭据摘要。v5 起也清除待审批和生效的安装，v6 起的待交付命令事件与去重摘要随安装清除。v7 起已发送的 Bot 回复和作者身份随群历史保留。v8 起副本保留快照中的账号状态和密码散列，清除恢复码。v9 起维护审计随快照保留操作时间，v10 起保留举报和结案状态。恢复后继续按原操作或结案时间计算查询窗口。恢复后由 Bot 所有者重新签发凭据，群主按需重新安装，Bot 使用新安装的游标。目标使用受保护的本地目录和 `0600` 的文件。命令拒绝覆盖现有数据库及其 SQLite 辅助文件。正常重启沿用原卷内的账号状态、有效会话、Bot 凭据、安装及有效事件。[本机维护审计](#maintenance-audit) · [普通群举报审阅与结案](#message-reports) · [账号恢复设计](../docs/topics/identity.md#auth-03-local-design)

在源实例的安装目录停止应用，将数据卷以只读方式交给带维护命令的候选镜像。备份仍含账号、密码散列、成员和消息正文。单独保存备份记录和原版本镜像标识，限制备份目录和副本的访问权限。

```sh
maintenance_image=radishchat:p1-candidate
docker compose --env-file instance.env stop chat
source_container=$(docker compose --env-file instance.env ps -aq chat)

umask 077
backup_dir=$(mktemp -d)
if docker run --rm --network none --volumes-from "$source_container:ro" \
  --entrypoint sh "$maintenance_image" \
  -ec 'radishchat backup /tmp/chat.sqlite && cat /tmp/chat.sqlite' > "$backup_dir/chat.sqlite.part"; then
  mv "$backup_dir/chat.sqlite.part" "$backup_dir/chat.sqlite"
else
  rm -f "$backup_dir/chat.sqlite.part"
  exit 1
fi
backup_file="$backup_dir/chat.sqlite"
```

普通备份完成后可启动原实例。升级时保持停服，先验证备份可恢复，再用新镜像打开原卷。只在隔离环境演练故障；迁移失败时保留故障卷供诊断，使用升级前备份恢复到新卷，并使用能读取该数据库版本的原镜像恢复服务。

恢复时将 `compose.yaml` 和该实例的部署配置复制到新目录。确认新项目名独立、尚未启动应用，复验时使用独立端口及对应的公开 URL。目标配置中的 `RADISHCHAT_IMAGE` 指向要启动的版本，维护操作仍使用上面的 `maintenance_image` 和备份的绝对路径。

```sh
# 在新的安装目录运行，使用该次恢复专用的项目名。
recovery_compose() {
  docker compose -p radishchat-recovery --env-file instance.env "$@"
}

recovery_compose create --no-build chat
target_container=$(recovery_compose ps -aq chat)
docker run --rm -i --network none --volumes-from "$target_container" \
  --entrypoint sh "$maintenance_image" \
  -ec 'umask 077; cat > /tmp/restore.sqlite; exec radishchat restore /tmp/restore.sqlite' < "$backup_file"
recovery_compose up -d --no-build --wait
```

只在 `restore` 成功后启动新实例。确认旧会话访问 `/api/v1/me` 返回 401，然后用可用账号重新登录，核对账号、群成员和历史消息。开放给成员前，对照快照后的停用和改密记录，重新应用仍然有效的停用，为需要废止快照密码的账号发起恢复。待恢复账号由维护者核实后签发新码。使用 Bot 的实例还应核对历史回复仍显示原作者，确认旧凭据访问 `/api/v1/bot/me` 返回 401。重新签发并安装后，Bot 使用新安装的游标读取后续命令。将确认通过的实例接回反代入口。备份保存在长期受保护的存储中，按实例的数据留存要求管理。[本地账号停用与恢复](#local-account-disable) · [Bot Web 操作](../docs/topics/bots.md#bot-01-web)

## 双实例验收

从仓库根目录创建一个干净安装目录，仅复制 Compose 验收配置和 Caddy 配置。该配置使用 Caddy 内部 CA、合成邀请码和两个独立命名卷。Caddy 只映射到本机回环地址，两个应用不发布端口。该环境供本机内测复跑，使用公开域名和公认 CA 的部署需在目标主机另行验收。

```sh
acceptance_dir=$(mktemp -d)
cp deploy/compose.acceptance.yaml deploy/Caddyfile.acceptance "$acceptance_dir/"
cd "$acceptance_dir"
(
  umask 077
  printf 'RADISHCHAT_IMAGE=radishchat:p1-candidate\nRADISHCHAT_ACCEPTANCE_INVITE=%s\n' \
    "$(openssl rand -hex 24)" > acceptance.env
)

acceptance_compose() {
  docker compose -p "radishchat-$(basename "$acceptance_dir" | tr '[:upper:].' '[:lower:]-')" \
    --env-file acceptance.env -f compose.acceptance.yaml "$@"
}

acceptance_compose up -d --wait
acceptance_compose cp caddy:/data/caddy/pki/authorities/local/root.crt ./acceptance-ca.crt
for host in chat-a.radishchat.test chat-b.radishchat.test; do
  curl --noproxy "$host" --fail --show-error --silent --cacert ./acceptance-ca.crt \
    --resolve "$host:8443:127.0.0.1" "https://$host:8443/healthz"
done
```

用隔离浏览器环境将两个主机名解析到 `127.0.0.1`，并为该环境信任导出的测试 CA。访问 `https://chat-a.radishchat.test:8443` 和 `https://chat-b.radishchat.test:8443`。端口冲突时通过 `RADISHCHAT_ACCEPTANCE_PORT` 调整端口，并同步修改访问地址和 `curl` 命令。网段冲突时同时设置 `RADISHCHAT_ACCEPTANCE_SUBNET` 和该网段内的 `RADISHCHAT_ACCEPTANCE_PROXY_IP`。

1. 检查两个入口均能加载 Web，使用合成邀请码创建普通账号。检查不带邀请码的注册被拒绝。
2. 在 A 用两个账号建立私聊，验证双方收发。断开其中一个浏览器的网络，让在线账号继续发消息，重连后确认历史补齐并继续接收实时消息。进入 B 时应能独立登录并使用聊天。
3. `acceptance_compose up -d --wait --force-recreate --no-deps chat-a chat-b` 后，在两个入口重新登录，确认各自账号和消息仍可读取。检查 `acceptance_compose ps` 和应用内 `id`、`stat -c '%a %u %n' /data /data/chat.sqlite`，并检查容器日志是否记录了合成密码、邀请码、会话值或消息正文。
4. 在验收目录内运行 `acceptance_compose down --volumes`，删除该次验收容器、网络和合成数据卷。删除临时目录并移除隔离浏览器环境对测试 CA 的信任。候选标签可供后续验收复用。
