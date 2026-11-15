# 当前状态

更新日期 2026-11-15。

**当前阶段是 P1 Web 聊天与自部署内测的实施阶段。** 本地检查基线、合成预览 v1 样本、默认准入与本地会话、公网邀请码、私聊唯一性和成员操作、外部登录禁用复验已通过单项验收。[基线和迁移样本](../records/2026-11-14-p1-local-baseline.md) · [本地会话验收](../records/2026-11-15-auth-local-sessions.md) · [邀请码验收](../records/2026-11-15-auth-invite.md) · [会话与成员验收](../records/2026-11-15-msg-room-membership.md) · [外部登录禁用验收](../records/2026-11-15-auth-oidc-disabled.md)

群成员本次加入边界和 SQLite v2 迁移已联合通过验收。消息确认与重试、Markdown 正文与渲染、分页与补拉已通过单项验收。[迁移与历史授权记录](../records/2026-11-15-membership-sqlite-v2.md) · [消息确认与 Markdown 验收](../records/2026-11-15-message-confirmation-markdown.md) · [分页与补拉验收](../records/2026-11-15-message-paging-sync.md)

Web 本地准入与聊天主路径、发送恢复与撤权已通过单项验收。SQLite 当前版本为 v3。[桌面与窄屏 Web 验收](../records/2026-11-15-web-local-chat.md) · [Web 恢复与成员资格验收](../records/2026-11-15-web-recovery.md)

完整多阶段候选镜像和本机 HTTPS 双实例已通过单项验收。Linux ARM64 候选标签为 `radishchat:p1-ops-01-03-20261115`。[部署说明](../../deploy/README.md) · [候选镜像与 HTTPS 验收](../records/2026-11-15-candidate-https.md)

独立实例切换、备份恢复和固定 v1 样本的迁移失败回退已通过本机验收。本轮 Linux ARM64 候选标签为 `radishchat:p1-web-switch-restore-20261115`。[维护步骤](../../deploy/README.md#sqlite-backup-restore) · [验收记录](../records/2026-11-15-instance-switch-and-restore.md)

| 顺序 | 当前事项 | 出口 |
| --- | --- | --- |
| 1 | [CI 产品检查](../topics/foundation.md#base-03-02) | Go 和 Web 已接入 `Candidate Quality` 并通过本地验证。待复核及 GitHub Actions 实跑。[本地验证记录](../records/2026-11-15-ci-product-checks.md) |
| 2 | 按[首轮验收矩阵](../planning/first-web-slice.md#p1-acceptance)完成 P1 联合验收 | 用带检查结果的候选镜像验证目标 HTTPS 入口，复核聊天、固定样本迁移及备份恢复证据 |

真实 Radish 登录的独立验收延期。等 Radish 侧有稳定实例且具备测试发行方、独立客户端、受控测试身份与 HTTPS 回调后，再按 [AUTH-02](../topics/identity.md#auth-02)安排。P1 使用本地账号放行。

首个治理 PR 已合入默认分支 `master`，并快进回流至 `dev`。`Candidate Quality` 门禁的检查来源已绑定 GitHub Actions。维护者已确认 [RadishChat Source-Available License 1.0](../../LICENSE)。[远端治理记录](../records/2026-11-14-planning-baseline.md#remote-bootstrap) · [专题地图](../topics/README.md)
