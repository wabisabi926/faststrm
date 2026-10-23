# 构建

## 环境

- Go 1.25+
- Node.js 20+（前端）
- fnOS 打包需要 `fnpack`（见 `scripts/build-fnos.sh`）

## 本地构建

```bash
# 前端：产物直接写入 go:embed 目录
cd frontend
npm ci
npm run build

# 后端（当前平台）
cd ..
go build ./cmd/server

# 指定版本
go build -ldflags "-X 'main.version=v1.5.6' -X 'main.BuildDate=2026-10-23T00:00:00Z'" ./cmd/server
```

前端产物必须位于 `internal/web/spa/index.html`，否则嵌入的 Web UI 为空。

## Windows 测试包

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build \
  -trimpath \
  -ldflags "-X 'main.version=v1.5.6' -X 'main.BuildDate=...'" \
  -o dist/faststrm-windows-amd64-test-<date>-<version>.exe \
  ./cmd/server
```

## 飞牛 fpk

```bash
export SCRIPTS=$(cygpath -u "$PWD/scripts")   # Windows Git Bash
BUILD_FNOS_QUIET=1 FASTSTRM_VERSION=1.5.6 PATH="$SCRIPTS:$PATH" ./scripts/build-fnos.sh all
```

`FASTSTRM_VERSION` 用于未打 tag 的预发布；否则脚本会回落到最新 git tag。

## 迁移工具

```bash
go build -o dist/faststrm-migrate.exe ./cmd/migrate
```
