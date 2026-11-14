# 仓库治理与检查

[分支决策](../adr/0002-branch-pr-rulesets.md)定义稳定主线、开发分支、合并和回流。[贡献指南](../../CONTRIBUTING.md)说明提交方式。仓库管理员的远端操作步骤放在 [Rulesets 说明](../../.github/rulesets/README.md)。

| 资产 | 职责 |
| --- | --- |
| `.editorconfig` 与 `.gitattributes` | 统一自有文本 UTF-8、LF 和结尾换行，Go 格式由 gofmt 管理 |
| `.gitignore` | 排除依赖、构建、测试、SQLite、运行数据和本机配置 |
| `AGENTS.md` 与 `CLAUDE.md` | 全文一致的协作入口，由仓库检查校验同步 |
| [许可](../../LICENSE)、贡献指南、行为规范与安全政策 | 说明项目许可、贡献、社区行为与私密报告路径 |
| Issue Forms 与 PR 模板 | 提供能定位问题的最小上下文、变更意图、验证和回流安排 |
| Ruleset 模板与 PR 工作流 | 约定稳定主线保护与 `Candidate Quality` |
| `scripts/check-repo.py` | 检查必需文件、活跃 Markdown 链接、文本卫生，以及本地声明的模板和 CI 契约 |

## CI 契约

`PR Checks` 在目标为 `dev` 或 `master` 的 PR 上运行。规划阶段有 `Repo Hygiene` 与聚合 `Candidate Quality` 两个 job。治理检查覆盖所有活跃文档与日常文件，PR 环境额外检查实际提交范围和空白差异。聚合等待组件结束，组件失败、取消或跳过时视为未通过。工作流只读仓库权限，不需要环境密钥。

Go、React、真实联调及发布检查按 [BASE-03](../topics/foundation.md#base-03) 与产品阶段逐步建立。当前探索源码可运行开发机检查，正式发布条件见 [OPS-03](../topics/deployment.md#ops-03)。文档与 CI 修改完成后，可依次运行：

```sh
python3 scripts/check-repo.py
```

已有 Git 历史时，可指定比较基线：

```sh
python3 scripts/check-repo.py --base <base-sha> --head <head-sha>
```

仓库可见性由维护者确定。项目使用与贡献授权以[根 LICENSE](../../LICENSE)为准。正式发行时按实际产物核对第三方依赖的许可与须保留的通知。
