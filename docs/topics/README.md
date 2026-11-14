# 能力专题地图

[整体规划](../planning/project-plan.md)定义产品和阶段出口。本页按能力列出大专题及其唯一负责的子专题。[当前状态](../status/current.md)安排近期工作。子专题可用同一份文件的稳定锚点，细节增多时可迁到专页并保留原入口。

| 大专题 | 子专题 |
| --- | --- |
| [账号、身份与设备](identity.md) | [AUTH-01 本地账号、会话与准入](identity.md#auth-01) · [AUTH-02 Radish OIDC 登录](identity.md#auth-02) · [AUTH-03 账号恢复、停用与注销](identity.md#auth-03) · [AUTH-04 设备与会话管理](identity.md#auth-04) |
| [会话、消息与同步](messaging.md) | [MSG-01 私聊、群组与成员权限](messaging.md#msg-01) · [MSG-02 收发、离线补拉与历史](messaging.md#msg-02) · [MSG-03 Markdown 与正文格式](messaging.md#msg-03) · [MSG-04 常用聊天能力与媒体](messaging.md#msg-04) |
| [Web 与 Flutter 客户端](clients.md) | [WEB-01 Web 基础聊天工作区](clients.md#web-01) · [WEB-02 可访问性与隐私提示](clients.md#web-02) · [APP-01 Flutter 实例接入与凭据](clients.md#app-01) · [APP-02 移动同步、通知与发行](clients.md#app-02) |
| [自部署与交付](deployment.md) | [OPS-01 单实例部署、备份与升级](deployment.md#ops-01) · [OPS-02 实例治理、诊断与隐私](deployment.md#ops-02) · [OPS-03 版本兼容与正式发布](deployment.md#ops-03) |
| [Bot 开放接口](bots.md) | [BOT-01 Bot 身份、安装与授权](bots.md#bot-01) · [BOT-02 事件、命令和示例 Bot](bots.md#bot-02) |
| [端到端加密与消失消息](privacy.md) | [LOCK-01 协议选型与加密私聊](privacy.md#lock-01) · [LOCK-02 限时消息与阅后消失](privacy.md#lock-02) · [LOCK-03 加密群与高级恢复](privacy.md#lock-03) |
| [工程基础与仓库治理](foundation.md) | [BASE-01 协作、文档、分支与 Rulesets](foundation.md#base-01) · [BASE-02 P1 首片方案与验证矩阵](foundation.md#base-02) · [BASE-03 技术基线与持续集成](foundation.md#base-03) |

子专题编号用于引用和验收追溯。多个专题协作时，任务和状态仍由一个主子专题持有。将来激活远期项时，在原条目补齐输入输出、失败与恢复路径、接口来源、验收和下一步。
