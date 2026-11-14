# GitHub Rulesets 启用说明

[master-protection.json](master-protection.json) 为稳定主线提供模板。此目录随源码维护。实际生效状态以目标 GitHub 仓库设置和实测为准。

## 管理员启用次序

1. 确认目标仓库与可见性，确认平台支持 Rulesets。确定初始提交的内容与许可。建立 `master` 及同一基线上的 `dev`，在 GitHub 将 `master` 设为默认分支。
2. 确认工作流已在仓库中。将一次真实测试 PR 指向 `master`，观察 GitHub Actions 产生 `Candidate Quality`。核对来源是 GitHub Actions 及预期的工作流。
3. 在 Merge options 开启 merge commit 和 rebase merge，关闭 squash。关闭自动删除 head 分支的选项，保留长期 `dev`。常规开发 PR 创建者显式选择 `dev` 为目标。
4. 导入模板，核对仓库、唯一匹配的分支、无绕过主体、审批数、review 对话和 required check。界面能指定状态检查来源时选择 GitHub Actions。活跃旧保护规则如有重叠，先比较再决定保留策略。
5. 验证 `master` 需要 PR 、基线需同步、候选检查须通过，且允许的合并方式与仓库一致。正常阶段 PR 完成后按 [ADR 0002](../../docs/adr/0002-branch-pr-rulesets.md)回流。记录仓库、规则 ID、核对日期和实际检查出处。

管理员变更前可保存现有规则和合并设置的快照。配置失误时按快照恢复，并重新验证保护和 required check。模板不启用签名提交、CODEOWNERS 审批、标签保护或自动发布。后续采用时按真实维护需求独立评审。

参考 [GitHub Rulesets 规则](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets)与[创建仓库 Ruleset 的 API](https://docs.github.com/en/rest/repos/rules#create-a-repository-ruleset)。
