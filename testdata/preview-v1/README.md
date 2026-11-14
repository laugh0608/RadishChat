# RadishChat SQLite v1 迁移样本

`source.tar.gz` 固定了首次迁移验收的预览源码、Go 依赖校验文件和 Web 锁文件。`source.sha256` 列出归档内每个文件的 SHA-256。`generate.py` 启动固定预览的本机回环实例，通过实际 API 写入合成数据，正常停服后再以只读方式检查数据库。

在项目根目录执行。首次构建可按 `go.mod` 和 `go.sum` 恢复依赖，已有完整模块缓存时可设置 `GOPROXY=off`。

```sh
project_dir="$PWD"
fixture_work="$(mktemp -d)"
tar -xzf "$project_dir/testdata/preview-v1/source.tar.gz" -C "$fixture_work"
(
  cd "$fixture_work" &&
  shasum -a 256 -c "$project_dir/testdata/preview-v1/source.sha256" &&
  GOTOOLCHAIN=local go build -mod=readonly -trimpath -o ./radishchat-preview-v1 ./cmd/radishchat
)
```

校验和构建成功后生成样本。Python 3 需要标准库 `sqlite3`。

```sh
python3 "$project_dir/testdata/preview-v1/generate.py" \
  --binary "$fixture_work/radishchat-preview-v1" "$fixture_work/preview-v1.sqlite"
python3 "$project_dir/testdata/preview-v1/generate.py" "$fixture_work/preview-v1.sqlite"
```

验证结果应为 `user_version=1`、3 个账号、2 个会话、5 条成员记录、5 条消息和 3 条合成会话记录。群消息跨越首次加入前、首次在群、离群和重新加入四段。预览 v1 只保存当前成员关系，四段顺序由样本生成步骤和消息标识标记，供后续迁移验证旧成员数据的授权边界。

逻辑 SHA-256 为 `735884915d1fce1f40f38f88874445114d7bf81e0c2d1bfaf9711ac06b89791f`。逻辑摘要覆盖 schema、稳定 ID、成员、正文、请求标识及会话计数等。密码盐、随机会话令牌摘要和时间戳随运行变化，原文件使用独立的字节摘要。开始迁移前将 v1 样本复制到独立路径，对副本执行迁移。生成实例由脚本在结束时停止。

环境、检查日志位置和首个原样本身份见[验收记录](../../docs/records/2026-11-14-p1-local-baseline.md)。
