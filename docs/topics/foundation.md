# 工程基础与仓库治理

维护协作规则、分支 PR、文档流程、基础工程主张和验证证据。技术约束随产品合同进入正式实施。

阶段验收目标：P0 有可维护的治理模板、明确的阶段接续和可评审首片。远端规则启用后另行核验。

本页维护各子专题的设计与执行状态。跨专题顺序见[整体规划](../planning/project-plan.md)，近期优先级见[当前状态](../status/current.md)。

<a id="base-01"></a>

## BASE-01 协作、文档、分支与 Rulesets

- 阶段与状态：P0。远端分支与 Ruleset 已启用，必需检查已绑定 GitHub Actions。首个治理 PR 已合并并快进回流，项目许可已定稿。[远端治理记录](../records/2026-11-14-planning-baseline.md#remote-bootstrap) · [项目许可证](../../LICENSE)
- 范围：固化三级文档、PR 与回流、Ruleset、故障与建议模板、报告边界和本地检查。
- 依赖与基础：参考五个兄弟项目并按 RadishChat 的阶段取舍。
- 验收：文档链与治理文件通过本地检查。远端启用按仓库治理另记生效证据。

<a id="base-02"></a>

## BASE-02 P1 首片方案与验证矩阵

- 阶段与状态：P0。评审完成，首批合同和采用决定已确认。P1 实施待开始。
- 评审结论：沿用 Go + React、单进程 SQLite、同源 Web 和 REST/SSE。在现有实现上整改注册准入、群历史授权、断线与撤权后的客户端状态、数据库迁移及恢复。真实 Radish 登录由 [AUTH-02](identity.md#auth-02) 延期追踪。
- 归属：[首个 Web 批次合同](../planning/first-web-slice.md)维护放行条件、八项预览采用清单和跨专题验收矩阵，并汇总[建议开工顺序](../planning/first-web-slice.md#p1-start-order)。实施任务和进度由各主子专题维护。[LOCK-01](privacy.md#p1-privacy-boundary) 提供 P1 隐私边界。
- P0 验收：维护者确认了注册准入、加群历史和迁移恢复三项决定，评审结果已写回子专题。P1 的运行结果将在候选版本上按矩阵逐项验收。

<a id="base-03"></a>

## BASE-03 技术基线与持续集成

- 阶段与状态：P1。本地产品检查基线已验收，CI 产品检查待开始。
- 范围与预览采用：沿用 Go 集成测试、Web 类型检查与生产构建。将与首批合同匹配的 Go 和 Web 检查纳入 `Candidate Quality`，补齐关键边界回归，并保留完整候选镜像构建证据。
- 验收：从干净检出按锁定依赖完成 Go 检查、Web 生产构建及候选镜像构建。稳定主线接收正式代码前，产品检查在 CI 可复现并由 `Candidate Quality` 汇总。浏览器与部署恢复按候选版本分别保留证据。[P1 验收矩阵](../planning/first-web-slice.md#p1-acceptance)

### 首批实施任务

| 任务 | 前置任务 | 改动范围 | 单项验收 | 状态 |
| --- | --- | --- | --- | --- |
| <a id="base-03-01"></a> BASE-03-01 本地产品检查基线 | BASE-02 已确认 | 核对 `go.mod`、`go.sum`、`web/package-lock.json` 与现有测试和构建入口，记录 Go、Node 及 npm 版本和本机命令 | 在干净工作目录按锁定依赖运行仓库治理、Go 测试及 `go vet`、Web 类型检查及生产构建。记录工具版本、结果和预览基线已有缺口 | 已完成。[验收记录](../records/2026-11-14-p1-local-baseline.md) |
| <a id="base-03-02"></a> BASE-03-02 汇总 CI 产品检查 | BASE-03-01 | 更新 `.github/workflows/pr-check.yml`，将 Go 和 Web 检查接入 `Candidate Quality`，保留现有仓库治理检查和 PR 入口 | 验证各必需检查成功时汇总通过，失败、取消或跳过时汇总失败。GitHub CI 启用后记录实际候选运行结果 | 待开始 |
