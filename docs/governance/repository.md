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

`PR Checks` 在目标为 `dev` 或 `master` 的 PR 上运行，使用只读仓库权限且无需环境密钥。

| 作业 | 必需检查 |
| --- | --- |
| `Repo Hygiene` | 运行 `scripts/check-repo.py`，检查活跃文档、治理契约、PR 提交范围和空白差异。 |
| `Go Checks` | 按 `go.mod` 选用 Go，运行 `go test -mod=readonly -count=1 -race ./...` 和 `go vet -mod=readonly ./...`。 |
| `Web Checks` | 使用 Node.js 24，在 `web/` 从锁文件执行 `npm ci --no-audit --no-fund`，再用 `npm run build` 依次执行 TypeScript 类型检查与 Vite 生产构建。 |

`Candidate Quality` 等待三个作业结束，仅当它们都返回 `success` 时通过。作业返回 `failure`、`cancelled` 或 `skipped`，汇总步骤即以非零状态退出。[GitHub Actions 依赖结果](https://docs.github.com/en/actions/reference/workflows-and-actions/contexts#needs-context)

稳定化 PR 应记录候选源码、镜像和实际验收范围。候选镜像、浏览器和部署证据见 [P1 验收矩阵](../planning/first-web-slice.md#p1-acceptance)及 [P2 本机联合验收](../records/2026-11-17-p2-local-joint-acceptance.md)。远端门禁以待合入 PR 上的 `Candidate Quality` 结果为准，正式发布条件见 [OPS-03](../topics/deployment.md#ops-03)。文档与 CI 修改可运行以下仓库检查。

```sh
python3 scripts/check-repo.py
```

已有 Git 历史时，可指定比较基线。

```sh
python3 scripts/check-repo.py --base <base-sha> --head <head-sha>
```

仓库可见性由维护者确定。项目使用与贡献授权以[根 LICENSE](../../LICENSE)为准。正式发行时按实际产物核对第三方依赖的许可与须保留的通知。
