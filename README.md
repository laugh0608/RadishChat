# RadishChat · 萝卜聊天

RadishChat 是 Radish 系列独立的在线即时通讯软件，专注日常私聊、小群聊天和 Markdown 消息。每个实例可以自部署并开放本地注册，也可配置 Radish 登录。当前采用 Go 服务端和 React Web，后续计划提供可连接用户指定实例的 Flutter 客户端。

P1 已通过本机联合验收并合入稳定主线。P2 候选已通过本机 HTTPS 双实例联合验收，覆盖独立 Bot 接入、账号恢复、举报与数据治理、快照恢复和 SQLite v11 升级。远端稳定化与目标实例内测待安排。[当前状态](docs/status/current.md) · [P2 验收记录](docs/records/2026-11-17-p2-local-joint-acceptance.md) · [运行示例 Bot](examples/pingbot/README.md)

| 要了解什么 | 从这里开始 |
| --- | --- |
| 产品边界、阶段和验收出口 | [项目整体规划](docs/planning/project-plan.md) |
| 大专题、子专题及设计与进度入口 | [能力专题地图](docs/topics/README.md) |
| 当前顺序、待决定事项和已知证据 | [当前状态](docs/status/current.md) |
| 协作、分支、PR、CI 和 Rulesets | [协作约定](AGENTS.md) · [贡献指南](CONTRIBUTING.md) · [仓库治理](docs/governance/repository.md) |
| 查看原型和本地验证 | [探索预览启动说明](docs/records/2026-11-14-preview-quickstart.md) · [原型验证记录](docs/status/2026-11-14-preview-verification.md) |
| 部署候选镜像和复跑 HTTPS 验收 | [部署说明](deploy/README.md) |
| 完整导航 | [文档入口](docs/README.md) |

## 许可

本项目原创软件和文档采用 [RadishChat Source-Available License 1.0](LICENSE)。许可允许个人、受控群组和组织内部自部署。商业托管、代运营、开放注册的公共服务和软件再分发需单独授权。第三方材料保留各自的许可。
