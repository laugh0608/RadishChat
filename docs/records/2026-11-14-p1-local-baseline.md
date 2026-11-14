# P1 本地检查基线与预览 v1 样本

2026-11-14 完成 [BASE-03-01](../topics/foundation.md#base-03-01) 与 [OPS-01-01](../topics/deployment.md#ops-01-01)。在干净的源码副本中用合成数据及隔离数据目录验收。工作区尚无 Git 历史，固定版本以归档和逐文件 SHA-256 标识。

## 本地检查

环境为 macOS 27.0.1 arm64、Go 1.26.3、Node.js 24.16.0、npm 11.13.0、Python 3.9.6。Go 和 npm 使用复制到临时目录的依赖缓存，锁文件与源码在验证后保持字节一致。

| 检查 | 实际命令与结果 |
| --- | --- |
| 仓库治理 | `python3 scripts/check-repo.py` 与 `cmp AGENTS.md CLAUDE.md` 通过 |
| Go 校验 | 使用 `GOTOOLCHAIN=local`、`GOPROXY=off` 和临时模块及构建缓存。`go mod verify`、`go test -mod=readonly -count=1 -race ./...`、`go vet -mod=readonly ./...` 均通过。测试覆盖预览收发、成员权限、持久化、分页和合成 OIDC |
| Go 构建 | `go build -mod=readonly -trimpath -o ../artifacts/radishchat-preview-v1 ./cmd/radishchat` 通过。解包归档后用相同工具链重建，两个本机二进制经 `cmp` 一致 |
| Web 构建 | `npm --prefix web ci --offline --cache "$PWD/../npmcache" --no-audit --no-fund` 安装 123 个包。`npm --prefix web run build` 完成 `tsc --noEmit` 和 Vite 8.3.8 生产构建 |

完整日志保存在本机 `output/p1/preview-v1/logs/`。本次覆盖使用已有缓存的干净工作目录。候选镜像完整构建、HTTPS 环境、首批功能与 GitHub CI 的实测结果由后续任务记录。此前预览的多阶段镜像构建缺口见[开发预览验证](../status/2026-11-14-preview-verification.md)。

## 迁移样本

用固定预览从两个干净目录分别造数，再从归档重建后第三次造数。三次只读校验均通过，`integrity_check` 返回 `ok`，`foreign_key_check` 返回空结果。逻辑摘要一致。样本含 Alice、Bob、Carol，1 个私聊、1 个群、5 条成员记录、5 条 Markdown 消息和 3 条合成会话。Carol 的加入、退出和重新加入均通过预览 API 操作。SQLite 校验使用 Python 所链接的 SQLite 3.54.0。

| 对象 | SHA-256 |
| --- | --- |
| `testdata/preview-v1/source.tar.gz` | `dc5097060508298c7dedca384f89182bf0b857e8c317d678a9140708027a7b72` |
| `testdata/preview-v1/source.sha256` | `9dce3d878dae8b5cac2af8b46eb99eb41170ed648d52f0cf8862cc09f4cda053` |
| macOS arm64 预览二进制 | `db8bd971c28350f1108762b72ce36a068fc18f764f5a220f13e282d5bab255c4` |
| 本机只读原样本 `output/p1/preview-v1/radishchat-preview-v1.sqlite` | `659c7c324189616b36af07b098337103b1a4ad21d3314824ef93bd75d2469129` |
| 样本逻辑摘要 | `735884915d1fce1f40f38f88874445114d7bf81e0c2d1bfaf9711ac06b89791f` |

原样本及日志位于本地忽略目录。归档、源码清单和生成脚本位于 `testdata/preview-v1/`，可用相同预览源码独立重建。后续迁移验收对副本操作。[复跑说明](../../testdata/preview-v1/README.md)
