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
