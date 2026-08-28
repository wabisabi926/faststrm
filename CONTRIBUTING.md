# 贡献指南

感谢你对 FastStrm 的兴趣！欢迎通过 Issue、PR 或社区反馈参与本项目。

## 🐛 反馈问题

提交 Issue 前请：

1. 搜索现有 Issue，避免重复
2. 在最新 `go` 分支上复现问题
3. 提供复现步骤、日志截图、运行环境（Docker / fNOS / 原生二进制、操作系统、115 客户端类型）
4. 涉及 Cookie / 账号问题时，**截图前请打码敏感信息**

## 🛠️ 本地开发

### 环境要求

- Go 1.25+
- Node.js 20+
- 可选：Docker（用于验证 Dockerfile 构建）

### 启动开发

```bash
git clone -b go https://github.com/wabisabi926/faststrm.git
cd faststrm

# 启动后端
go run ./cmd/server/

# 另开终端启动前端 dev server（HMR）
cd frontend
npm install
npm run dev
```

### 测试与质量检查

提交前请确保以下命令全部通过：

```bash
# Go 单元测试
go test ./... -race

# golangci-lint（需先 go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8）
golangci-lint run --config .golangci.yml ./...

# 前端测试
cd frontend && npx vitest run
```

CI 会在 push / PR 时自动跑这三套检查，本地预先通过可节省往返时间。

## 📝 提交规范

- 使用 [Conventional Commits](https://www.conventionalcommits.org/) 风格：
  - `feat: 新增 ...`
  - `fix: 修复 ...`
  - `refactor: 重构 ...`
  - `docs: 文档 ...`
  - `chore(release): bump version to vX.Y.Z`
- 一次提交聚焦一个改动点，避免混合无关变更
- **不要把本地调试脚本、缓存文件、覆盖率产物提交进仓库**（参考 `.gitignore` 中的 `tools/`、`*.out`、`.golangci-cache/` 等）

## 📦 发版流程

1. 更新 `cmd/server/main.go` 与 `frontend/package.json` 的 `version` 字段（保持一致）
2. 在 `.changes/vX.Y.Z.md` 写更新日志，同步更新 `wiki_drafts/版本更新日志.md`
3. 提交并发 tag：`git tag -a vX.Y.Z -m "vX.Y.Z" && git push origin vX.Y.Z`
4. GitHub Actions 的 `release.yml` 会自动构建 Docker 镜像、飞牛 `.fpk`、原生二进制并发布 GitHub Release

## 🤝 行为准则

请保持友善、尊重的交流态度。详细社群入口见 [Readme.md](Readme.md) 的「交流社群」章节。
