# ADR 0002 分支、PR 与 Rulesets

日期 2026-11-14。状态为规划基线。远端按[启用说明](../../.github/rulesets/README.md)落地。

## 分支职责

`master` 是 GitHub 默认分支及稳定基线。日常串行开发在 `dev`，按改动范围做本地验证。需要外部贡献、并行工作、正式评审或隔离风险时，用 `feature/*`、`fix/*`、`docs/*`、`chore/*` 或 `experiment/*` 向 `dev` 发 PR。紧急稳定版修复可以用 `hotfix/*` 直接向 `master` 发 PR，修复资料和回流按同一流程处理。

## 稳定化与回流

`dev → master` 按完整批次开 PR。合并优先采用 merge commit，以保留原提交身份与快进回流条件。允许 rebase merge，禁用 squash merge。仓库使用 Conventional Commits。PR 范围内 Git 自动产生的 merge commit 以父提交拓扑识别。

任何 PR 合入 `master` 后，先把最新 `origin/master` 回流到 `dev`，再开始下一批开发：

1. `git fetch origin`，确认工作区干净后切到 `dev`，将远端 `dev` 快进到本地。
2. 优先用 `git merge --ff-only origin/master`。无法快进时检查两侧历史，用普通 merge 解决冲突，并按受影响范围验证。
3. 执行 `git merge-base --is-ancestor origin/master HEAD`，成功后推送 `dev`。共享分支历史保持可追溯，回流过程中禁止 reset、rebase 或 force push。

纯历史回流记录提交图，实际内容变化记录相应验证。回流与发布各自记录。

## 保护与检查

- `master` 只收 PR，阻止删除和强推，要求解决 review 对话。单人维护阶段审批数量为零。聚合检查 `Candidate Quality` 必须成功，且分支必须跟上基线。
- 模板仅匹配 `refs/heads/master`。常态规则无绕过主体。多人稳定维护、持续外部贡献或出现绕过检查的回归时，评估增加独立审批和 `dev` 保护。
- `dev` 的 PR 获得相同 CI 反馈。普通 `dev` push 沿用本地验证。各项目自己的构建组件可以变化，通过同一个聚合检查向 Ruleset 报告。
- 初始 CI 是规划阶段的治理基线。正式产品变更晋级前，在 [BASE-03](../topics/foundation.md#base-03) 中把实际 Go 和 React 基线纳入聚合。

采用前需核对 Ruleset JSON、GitHub 仓库合并选项、PR 模板和 CI 名称。分支与规则变更同步本 ADR 及[仓库治理](../governance/repository.md)。
