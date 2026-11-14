# 参与 RadishChat

当前工作进度见[当前状态](docs/status/current.md)。先从[整体规划](docs/planning/project-plan.md)和[专题地图](docs/topics/README.md)找归属。讨论用实际聊天、成员管理、自部署或客户端场景描述问题。带上可验证的结果和预期行为。

- 常规串行开发在 `dev`，需要独立评审的贡献从 `dev` 建主题分支，PR 也以 `dev` 为目标。
- 提交使用 Conventional Commits，如 `docs(plan): define the first chat milestone`。PR 模板说明用户场景、变更行为、风险、验证以及需同步的文档。
- 运行 `python3 scripts/check-repo.py`。产品改动根据对应子专题补充测试和环境信息。稳定化、紧急修复与回流遵循 [Git 决策](docs/adr/0002-branch-pr-rulesets.md)。
- 公开提交使用合成用户名、测试消息和脱敏日志。安全问题走[私密报告路径](SECURITY.md)。

社区行为以[行为准则](CODE_OF_CONDUCT.md)为准。提交贡献前确认你有权提交相关材料。提交的贡献按[项目许可证](LICENSE)第 5 节授权，另有书面约定的除外。第三方材料须说明来源并遵守各自的许可。
