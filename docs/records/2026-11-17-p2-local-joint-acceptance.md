# P2 候选版本本机联合验收

2026-11-17，以 `fd6232de84e026bda2eb83cf29cbfc0299875d34` 为 P2 候选，文件树为 `016b57bdc80e10c41c394ddd582e156de733134e`。从 `git archive` 解包构建，使用仓库 Dockerfile，镜像标签 `radishchat:p2-candidate-fd6232d`，本机镜像 ID `sha256:ec1cf784b49a62cf0299d5d007816415db07f2e71c679a30bd651bbf3c82b7f0`，平台为 `linux/arm64`。镜像的源码标签与候选提交相符。[P2 阶段出口](../planning/project-plan.md#里程碑与阶段出口) · [候选构建与部署](../../deploy/README.md)

## 联合验收

本机为 macOS 27.0.1 arm64、Docker 29.8.2、Compose 5.5.0。两个实例运行同一镜像，在独立命名卷上初始化 SQLite v11，使用 Caddy 2.11.4 和各自的 HTTPS 测试主机名。API 客户端验证测试 CA 与主机名，Playwright CLI 0.1.24 驱动隔离的 Chromium 会话，在 390 × 844 视口下仅接受已核验的测试证书公钥。所有账号、凭据、举报及消息使用合成数据。

| P2 放行场景 | 同一候选的实测结果 |
| --- | --- |
| 受控准入与实例隔离 | 两个 HTTPS 入口均可加载候选 Web，缺失邀请码的注册被拒绝，合成邀请码可注册。两侧使用相同用户名、数字账号和群 ID，互换会话和 Bot 凭据均被拒绝。浏览器分别登录后只看到本实例会话。恢复 A 后关闭注册，已有成员仍可登录。 |
| Bot 接入、轮换与移除 | 群主与 Bot 所有者为不同账号，审批前事件拉取被拒绝。审批后，A 的 Go 示例及 B 的独立 Python 客户端各完成一次 `ping` → `pong`，候选 Web 显示 Bot 身份及回复。Bot 凭据访问真人聊天接口返回 `401`。A 轮换后旧凭据返回 `401`，新凭据可拉取。B 移除安装后拉取返回 `404`，原回复仍可读。 |
| 举报、停用与账号恢复 | 窄屏 Web 展示举报范围提示并返回举报编号。同一容器的 CLI 审阅所报消息，结案停用后旧会话及该作者的 Bot 凭据失效。维护者解除停用并签发恢复码，通过窄屏 Web 重置密码。新密码可登录、旧密码不可用、同一码不能再用，B 拒绝使用 A 的码。 |
| 导出、留存及恢复 | 本人导出逐字段核对，无法用不匹配的用户名导出。数据目录由 UID `10001` 持有，模式 `0700`，数据库与导出文件模式 `0600`。停服从只读源卷备份，恢复到 A 的新命名卷，按原结案时间补做停用和结案，再清理满 30 天的举报和满 90 天的审计。清理各删除 1 条并写入审计，重跑各删除 0 条。A 原会话及 Bot 凭据失效，其他成员重新登录可读原历史及 Bot 回复，B 历史继续可用。抽查恢复后的日志未发现测试凭据和所报正文。 |
| 固定 v1 样本升级 | 从固定预览源码重新生成合成样本，逻辑 SHA-256 与文档一致。经候选的恢复命令写入独立卷并启动，升至 v11，完整性和外键检查通过，账号、群成员和消息保留。迁移按旧群最大消息 ID 建立成员历史可见下限。原样本 SHA-256 在复验前后一致。 |

复跑入口为[双实例配置](../../deploy/README.md#双实例验收)、[独立 Bot 接入](2026-11-17-bot-independent-acceptance.md)、[账号恢复操作](../../deploy/README.md#local-account-disable)、[治理与快照恢复](2026-11-17-governance-data.md)及 [v1 样本](../../testdata/preview-v1/README.md)。验收后清理隔离浏览器、容器、网络、命名卷、测试证书和临时文件，保留本机候选镜像供复验。

## 门禁与交付状态

本机运行 `python3 scripts/check-repo.py --base 3af1b55 --head fd6232d`、Go 1.26.3 的 `go test -mod=readonly -count=1 -race ./...` 和 `go vet -mod=readonly ./...`、Node.js 24.16.0 的 `npm ci --no-audit --no-fund` 和 `npm run build`，均通过。验收时远端 `master` 为 `3af1b55`，属于候选历史，远端 `dev` 为 `3c44e16`，本地候选领先 8 个提交。[仓库门禁](../governance/repository.md#ci-契约)

P2 本机联合验收通过。远端稳定化仍需推送 `dev`、创建 `dev → master` PR 并等待 GitHub Actions 的 `Candidate Quality` 通过。合并后先回流 `dev`。受控内测实例需另行明确部署目标和准入范围，并在该环境核对 HTTPS、备份恢复及运维权限。[稳定化与回流](../adr/0002-branch-pr-rulesets.md#稳定化与回流) · [部署操作](../../deploy/README.md)
