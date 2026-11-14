# 规划与治理参照

调研日期 2026-11-14。本次查阅本机五个兄弟项目的协作约定、分支 ADR、Ruleset 和规划入口。以下提交用于标明各工作区阅读时的 HEAD。GitHub 远端规则的实际状态未用这些文件推定。

| 项目 | 阅读基线与来源 | 转化到 RadishChat 的做法 |
| --- | --- | --- |
| Radish | `f5cc7058` · `Docs/adr/0001-branch-and-pr-governance.md`、`Docs/guide/agent-collaboration.md`、`Docs/planning/topics.md` | 采用单人 `dev` 日常集成，阶段 PR 与回流。Radish 自身的 .NET 和宿主门禁留在原项目 |
| RadishMind | `3b1e664c` · `docs/adr/0001-branch-and-pr-governance.md`、`docs/agent-collaboration.md`、`docs/radishmind-roadmap.md` | 维护稳定聚合检查，功能合同和当前焦点分开。CI 组件按 Go 与 React 的正式采用范围建立 |
| RadishCatalyst | `f5492931` · `docs/process/agent-collaboration.md`、`docs/planning/current.md`、`.github/ISSUE_TEMPLATE/` | 整体阶段验收用可实测的用户路径，Issue 表单围绕具体问题和场景 |
| RadishFlow | `3a379fd9` · `docs/development/agent-collaboration.md`、`docs/status/current.md`、`docs/topics/README.md` | 采用整体规划、大专题、子专题三级结构，任务与证据各有唯一来源 |
| RadishAxiom | `9284b5a` · `docs/governance/agent-collaboration.md`、`docs/topics.md`、`docs/adr/0001-branch-and-pr-governance.md` | 区分设计决定与证据，稳定分支精确匹配，Conventional Commits 由 CI 处理，避免套用身份承诺或审批流程 |

[整体规划](../planning/project-plan.md)定义项目的拟议阶段。[分支决策](../adr/0002-branch-pr-rulesets.md)按初期单人维护使用独立 `master`，选用无常态绕过主体的保护模板。工具链、许可及正式部署通过各自专题评审。

早期探索代码、截图和[验证记录](../status/2026-11-14-preview-verification.md)为方案选择提供证据。首个正式实施批次的范围及探索代码采用方式由 [BASE-02](../topics/foundation.md#base-02)记录。


## 本地核对

仓库检查通过，覆盖治理文件、活跃文档链接和子专题唯一归属。工作流及 Issue YAML 可解析，Ruleset JSON 可解析。临时 Git 仓库的 PR 提交范围检查通过，错误提交主题和断开的活跃文档链接均被拒绝。真实 GitHub Ruleset 和 Actions 待远端启用后验收。

## 根协作文件专项复核

2026-11-14 逐份核对五个兄弟项目根目录的 `AGENTS.md` 和 `CLAUDE.md`。四组文件逐字一致，RadishCatalyst 仅首段的适用对象不同。以下提炼针对工作区中的文件内容。

| 项目 | 补入 RadishChat 的通用做法 |
| --- | --- |
| Radish | 任务入口保持稳定，按任务查正式文档。日常开发、稳定化和发布各自提供相应范围的验证证据 |
| RadishMind | 明确已授权工作可直接推进，说明依赖与运行操作的影响和清理方式。证据只支撑已覆盖的结论 |
| RadishCatalyst | 涉及用户可见交互时在相应运行环境验证真实路径。记录仍需复验的结果 |
| RadishFlow | 说明、诊断和评审默认只读。以任务路由定位文档，阶段事实和历史证据各归其位 |
| RadishAxiom | 开工核对工作区和已有改动，交接核对差异、未跟踪文件和未覆盖项。第三方材料核对来源及许可 |

维护者确认 [AGENTS.md](../../AGENTS.md) 与 [CLAUDE.md](../../CLAUDE.md) 全文一致，并沿用「萝卜SAMA」的称呼约定。更细的执行和验证规则放在[协作与验证](../governance/collaboration.md)。依赖恢复、临时服务和项目验证入口按 RadishChat 的实际任务执行。分支与远端规则仍由 [ADR 0002](../adr/0002-branch-pr-rulesets.md)定义。

后续将复核扩大到本机 16 个其他 Radish 系列项目的现行仓库入口，包含 `RadishIntelligence/RadishUI`。16 个项目都明确规定提交不添加 AI 署名。RadishChat 同步补入按授权精确暂存、按主题提交、保留共享历史、生成物可追溯及按实际证据交接等共用约定。

逐字节比较发现 11 组文件完全一致。RadishForge 和 RadishInk 的差异在标题，RadishCatalyst 和 RadishX 的差异在首段适用对象，RadishUI 的标题和首段均有差异。5 组均保留完整规则正文。RadishAxiomChecker 的两份入口均为 11 行且完全一致。
