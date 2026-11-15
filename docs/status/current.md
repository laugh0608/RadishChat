# 当前状态

更新日期 2026-11-15。

**当前阶段是 P1 Web 聊天与自部署内测的实施阶段。** 本地检查基线、合成预览 v1 样本、默认准入与本地会话、公网邀请码已通过单项验收。[基线和迁移样本](../records/2026-11-14-p1-local-baseline.md) · [本地会话验收](../records/2026-11-15-auth-local-sessions.md) · [邀请码验收](../records/2026-11-15-auth-invite.md)

| 顺序 | 当前事项 | 出口 |
| --- | --- | --- |
| 1 | 按[首批开工顺序](../planning/first-web-slice.md#p1-start-order)接续[私聊与成员操作](../topics/messaging.md#msg-01-01)及[外部登录禁用复验](../topics/identity.md#auth-02-01)。[CI 产品检查](../topics/foundation.md#base-03-02)可独立安排 | 按各子专题验证，迁移阶段使用已固定的 v1 样本副本 |

真实 Radish 登录的独立验收延期。等 Radish 侧有稳定实例且具备测试发行方、独立客户端、受控测试身份与 HTTPS 回调后，再按 [AUTH-02](../topics/identity.md#auth-02)安排。P1 使用本地账号放行。

此前探索版本的双浏览器、合成发行方和 Compose 运行镜像重建已有证据。完整多阶段镜像、目标环境 HTTPS、备份恢复及固定合成样本迁移待随 P1 候选版本验收。参见[预览验证](2026-11-14-preview-verification.md)与[首轮验收矩阵](../planning/first-web-slice.md#p1-acceptance)。

首个治理 PR 已合入默认分支 `master`，并快进回流至 `dev`。`Candidate Quality` 门禁的检查来源已绑定 GitHub Actions。维护者已确认 [RadishChat Source-Available License 1.0](../../LICENSE)。[远端治理记录](../records/2026-11-14-planning-baseline.md#remote-bootstrap) · [专题地图](../topics/README.md)
