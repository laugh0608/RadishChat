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

<a id="sqlite-backup-restore"></a>

## SQLite 备份、恢复与升级回退

候选镜像中的 `radishchat backup <备份文件>` 从 `RADISHCHAT_DATABASE` 生成快照，`radishchat restore <备份文件>` 向该变量指定的新路径恢复。命令可读取 v1 至 v5 并保留原库版本，检查 SQLite 完整性和外键，清除副本内的登录会话与未完成的 OIDC 流程。v4 起副本保留 Bot 定义及所有者，清除 Bot 凭据摘要。v5 起也清除待审批和生效的安装。恢复后由 Bot 所有者重新签发凭据，群主按需重新安装。目标使用受保护的本地目录和 `0600` 的文件。命令拒绝覆盖现有数据库及其 SQLite 辅助文件。正常重启沿用原卷内的登录会话、Bot 凭据和安装。[Bot 群安装 API](../docs/api/v1.md#bot-installations)

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

只在 `restore` 成功后启动新实例。确认旧会话访问 `/api/v1/me` 返回 401，然后重新登录，核对账号、群成员和历史消息。使用 Bot 的实例还应确认旧凭据访问 `/api/v1/bot/me` 返回 401，重新签发后只能访问重新授权的群。将确认通过的实例接回反代入口。备份保存在长期受保护的存储中，按实例的数据留存要求管理。[Bot Web 操作](../docs/topics/bots.md#bot-01-web)

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
