# 飞牛 fpk

## 构建

```bash
export SCRIPTS=$(cygpath -u "$PWD/scripts")   # Windows Git Bash
BUILD_FNOS_QUIET=1 FASTSTRM_VERSION=1.5.4 PATH="$SCRIPTS:$PATH" ./scripts/build-fnos.sh all
```

产物：

- `dist/faststrm-amd64-<version>.fpk`
- `dist/faststrm-arm64-<version>.fpk`

版本优先级：`FASTSTRM_VERSION` > 最新 git tag > manifest。未打 tag 的预发布必须设置 `FASTSTRM_VERSION`，否则会沿用上一个 tag。

## 包内检查

- `app/.config/.config.json` 的 `admin.password` 为空 → 首次启动进入 Setup Wizard。
- `internal/web/spa/index.html` 存在，否则包内没有 Web UI。
- amd64/arm64 的 manifest `platform` 分别为 x86/arm。

## 已知问题

- Windows 下 fnpack 路径含中文/空格时，必须用 `cygpath -u` 转换后再加入 PATH。
- 打包后如果前端文案又改了，需要重新 `npm run build` 并重打，不能只改源码。
