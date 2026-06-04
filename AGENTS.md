# AGENTS.md

## 覆盖率

- 本仓库测试放在 `tests/` 目录，不要求测试文件和源码同包放置。
- 不要直接用 `go test ./... -cover` 判断真实源码覆盖率；这种方式会让 `tests/...` 显示 `[no statements]`，并且不能反映仓库源码的整体覆盖情况。
- 计算仓库覆盖率使用：

```sh
go run ./cmd/lqcover
```

- 带最低覆盖率门槛时使用：

```sh
go run ./cmd/lqcover -threshold 70
```

- 工具会自动：
  - 发现源码包；
  - 排除 `tests/...` 和覆盖率工具自身；
  - 使用 `go test ./... -coverpkg=<源码包列表>` 生成原始 profile；
  - 合并重复 coverage block；
  - 输出真实 statement coverage。

- 默认生成 `coverage.out`，该文件已加入 `.gitignore`。
- 需要 HTML 报告时使用：

```sh
go run ./cmd/lqcover -html coverage.html
```

- 当前基准结果约为：

```text
coverage: 80.1% of statements (1033/1289)
```
