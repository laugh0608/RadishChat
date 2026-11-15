# P1 CI 产品检查本地验证

2026-11-15 在本地 `dev` 的 `8e54fed` 基础上验证 [BASE-03-02](../topics/foundation.md#base-03-02)。PR 目标仍为 `dev` 和 `master`，`Repo Hygiene` 继续检查实际 PR 提交范围。`Candidate Quality` 同时等待 `Repo Hygiene`、`Go Checks` 和 `Web Checks`，逐一要求 `success`。[工作流](../../.github/workflows/pr-check.yml) · [CI 契约](../governance/repository.md#ci-契约)

本机为 macOS 27.0.1 arm64，使用 Go 1.26.3、Node.js 24.16.0、npm 11.13.0、Python 3.14.5。Go 使用隔离的模块和构建缓存，Web 在 `8e54fed` 的干净源码副本中从锁文件离线安装依赖。

| 检查 | 实际结果 |
| --- | --- |
| Go | `go test -mod=readonly -count=1 -race ./...` 和 `go vet -mod=readonly ./...` 通过。 |
| Web | `npm ci --no-audit --no-fund` 安装 123 个包。`npm run build` 完成 `tsc --noEmit` 和 Vite 8.3.8 生产构建，生成 `dist/index.html` 和静态资源。 |
| 工作流 | `actionlint` 1.7.12 通过。对照 `8e54fed` 确认 PR 触发范围、权限和 `Repo Hygiene` 定义一致。 |
| 汇总 | 从 YAML 读取汇总步骤并在 Bash 中执行。覆盖 `success`、`failure`、`cancelled`、`skipped` 的 64 种组合，只有全部成功时退出码为 0。逐一缺失三个作业的结果时均退出 1。 |
| 仓库治理 | `python3 scripts/check-repo.py` 和 `git diff --check` 通过。以本地 `origin/dev` 和 `origin/master` 两个引用分别核对至 `8e54fed` 的提交范围，均通过。仓库检查拒绝了移除依赖、忽略失败或跳过必需步骤等 10 种工作流变更。 |

本地验证覆盖汇总步骤的退出状态。待变更复核后，通过实际 PR 记录 GitHub Actions 的作业调度及 `Candidate Quality` 检查结论。公网 HTTPS 联合验收继续按 [P1 验收矩阵](../planning/first-web-slice.md#p1-acceptance)安排。
