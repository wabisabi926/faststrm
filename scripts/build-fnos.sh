#!/usr/bin/env bash
# build-fnos.sh — 构建飞牛 (fNOS) 应用 .fpk 包
#
# 用法:
#   ./scripts/build-fnos.sh [amd64|arm64|all]
#
# 依赖:
#   - go (交叉编译 Linux 二进制)
#   - templ (生成模板代码，可选)
#   - fnpack (飞牛官方打包工具, Linux 版)
#       下载: https://static2.fnnas.com/fnpack/fnpack-1.2.1-linux-amd64
#       安装: chmod +x fnpack && sudo mv fnpack /usr/local/bin/
#   - bash (运行该脚本; Windows 请用 WSL / Git Bash)
#
# 产物: dist/faststrm-{arch}-{version}.fpk
#         dist/faststrm-{arch}-{version} (解压后的安装目录)
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

# 版本号优先取 git tag (vX.Y.Z -> X.Y.Z), 否则从 manifest 读
MANIFEST_VERSION="$(grep '^version' deploy/fnos/faststrm-amd64/manifest 2>/dev/null | sed 's/.*=[[:space:]]*//' | tr -d '\r\n' || true)"
TAG_VERSION="$(git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//' || true)"
VERSION="${TAG_VERSION:-${MANIFEST_VERSION:-dev}}"
BUILD_DATE="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

DIST_DIR="${ROOT_DIR}/dist"
mkdir -p "${DIST_DIR}"

ARCHES=("${1:-amd64}")
if [ "${ARCHES[0]}" = "all" ]; then
  ARCHES=(amd64 arm64)
fi

echo "==> FastStrm v${VERSION} (built ${BUILD_DATE})"
echo "    Target arches: ${ARCHES[*]}"

# 检查 fnpack
if ! command -v fnpack >/dev/null 2>&1; then
  echo "ERROR: fnpack not found in PATH." >&2
  echo "  Download: https://static2.fnnas.com/fnpack/fnpack-1.2.1-linux-amd64" >&2
  echo "  Install:  chmod +x fnpack && sudo mv fnpack /usr/local/bin/" >&2
  exit 1
fi

# templ generate (如果安装了 templ)
if command -v templ >/dev/null 2>&1; then
  echo "==> templ generate"
  (cd "${ROOT_DIR}" && templ generate)
fi

# 确保前端构建产物已就位 (internal/web/spa) —— embed 嵌入不需要额外动作
# 只做个轻量检查
if [ ! -f "${ROOT_DIR}/internal/web/spa/index.html" ]; then
  echo "WARN: internal/web/spa/index.html 未找到，请先构建前端 (cd frontend && npm i && npm run build)" >&2
  echo "      并把 frontend/dist/* 复制到 internal/web/spa/。否则二进制里没有 Web UI 页面。" >&2
fi

for ARCH in "${ARCHES[@]}"; do
  case "$ARCH" in
    amd64) GOARCH=amd64 ;;
    arm64) GOARCH=arm64 ;;
    *) echo "Unknown arch: $ARCH"; exit 1 ;;
  esac

  STAGE="${ROOT_DIR}/deploy/fnos/faststrm-${ARCH}"
  APP_DIR="${STAGE}/app"
  CMD_DIR="${STAGE}/cmd"
  MANIFEST="${STAGE}/manifest"

  mkdir -p "${APP_DIR}" "${CMD_DIR}"

  # --- 1. 同步 manifest 版本号到当前 VERSION (仅内存修改，写回用于打包) ---
  # 保留用户对 manifest 的本地修改，只替换 version= 行
  TMP_MANIFEST="$(mktemp)"
  sed -E "s/^version[[:space:]]*=[[:space:]]*.*/version = ${VERSION}/" \
    "${MANIFEST}" > "${TMP_MANIFEST}" && mv "${TMP_MANIFEST}" "${MANIFEST}"

  echo "==> [${ARCH}] building Go binary (GOOS=linux GOARCH=${GOARCH})"
  CGO_ENABLED=0 GOOS=linux GOARCH="${GOARCH}" \
    go build -trimpath \
      -ldflags="-s -w \
        -X 'github.com/wabisabi926/faststrm/internal/handler.appVersion=v${VERSION}' \
        -X 'main.version=v${VERSION}' \
        -X 'main.BuildDate=${BUILD_DATE}'" \
      -o "${APP_DIR}/faststrm" ./cmd/server/
  chmod +x "${APP_DIR}/faststrm"

  # --- 2. 拷贝默认配置模板 & entrypoint 到 app 目录 (与 Docker 镜像保持一致) ---
  echo "==> [${ARCH}] copy default config templates & entrypoint"
  cp -R "${ROOT_DIR}/.config"                "${APP_DIR}/.config"
  cp    "${ROOT_DIR}/docker-entrypoint.sh"    "${APP_DIR}/entrypoint.sh"
  chmod +x "${APP_DIR}/entrypoint.sh"

  # --- 3. 生成飞牛回调脚本 (cmd/main, install_callback, 等) ---
  echo "==> [${ARCH}] generate fNOS command scripts"

  # cmd/install_callback — 安装后回调: 保证持久目录存在 (无实际动作，Go 程序会自建)
  cat > "${CMD_DIR}/install_callback" <<'FNOS_EOF'
#!/bin/sh
# fNOS 会在应用包安装完成后调用
set -e
APP_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
LOG_DIR="${FNOS_APP_LOG_DIR:-${APP_ROOT}/logs}"
mkdir -p "${LOG_DIR}"
echo "[install_callback] faststrm installed at ${APP_ROOT}" >&2
exit 0
FNOS_EOF

  # cmd/uninstall_init — 卸载前回调: 先停进程
  cat > "${CMD_DIR}/uninstall_init" <<'FNOS_EOF'
#!/bin/sh
set -e
APP_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PIDFILE="${APP_ROOT}/faststrm.pid"
if [ -f "${PIDFILE}" ]; then
  PID="$(cat "${PIDFILE}" 2>/dev/null || true)"
  if [ -n "${PID}" ] && kill -0 "${PID}" 2>/dev/null; then
    kill -TERM "${PID}" 2>/dev/null || true
    sleep 1
    kill -KILL "${PID}" 2>/dev/null || true
  fi
  rm -f "${PIDFILE}"
fi
# 兜底：按进程名再杀一轮
pkill -f "${APP_ROOT}/app/faststrm" 2>/dev/null || true
echo "[uninstall_init] faststrm stopped" >&2
exit 0
FNOS_EOF

  # cmd/upgrade_init — 升级前: 停止旧版本
  cp "${CMD_DIR}/uninstall_init" "${CMD_DIR}/upgrade_init"

  # cmd/upgrade_callback — 升级后: 启动新版本 (通过 cmd/main)
  cat > "${CMD_DIR}/upgrade_callback" <<'FNOS_EOF'
#!/bin/sh
set -e
APP_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
exec "${APP_ROOT}/cmd/main"
FNOS_EOF

  # cmd/main — fNOS 应用主入口 (启动/重启 faststrm)
  cat > "${CMD_DIR}/main" <<'FNOS_EOF'
#!/bin/sh
# fNOS 应用启动入口。manifest.desktop_applaunchname / 服务模式都会调用此脚本。
set -eu
APP_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP_DIR="${APP_ROOT}/app"
PIDFILE="${APP_ROOT}/faststrm.pid"
LOG_DIR="${FNOS_APP_LOG_DIR:-${APP_ROOT}/logs}"
mkdir -p "${LOG_DIR}"
LOG_FILE="${LOG_DIR}/faststrm.log"

# 持久化数据目录：
#   - 优先使用 fNOS 透传的 FNOS_APP_DATA_DIR
#   - 否则用应用目录下的 data/
DATA_DIR="${FNOS_APP_DATA_DIR:-${APP_ROOT}/data}"
CONFIG_DIR="${FNOS_APP_CONFIG_DIR:-${APP_ROOT}/config}"
mkdir -p "${DATA_DIR}" "${CONFIG_DIR}"

# 如果上一次启动还在跑，先停掉
if [ -f "${PIDFILE}" ]; then
  OLD_PID="$(cat "${PIDFILE}" 2>/dev/null || true)"
  if [ -n "${OLD_PID}" ] && kill -0 "${OLD_PID}" 2>/dev/null; then
    kill -TERM "${OLD_PID}" 2>/dev/null || true
    sleep 1
  fi
  rm -f "${PIDFILE}"
fi
pkill -f "${APP_DIR}/faststrm" 2>/dev/null || true
sleep 0.5

export TZ="${TZ:-Asia/Shanghai}"
export DEFAULT_CONFIG_DIR="${CONFIG_DIR}"
# Go 程序会把 .strm、SQLite、jwt_secret 放到 DEFAULT_CONFIG_DIR/data
# 这里额外暴露独立的 DATA_DIR 环境变量供后续扩展
export APP_DATA_DIR="${DATA_DIR}"

cd "${APP_DIR}"
nohup \
  "${APP_DIR}/entrypoint.sh" "${APP_DIR}/faststrm" \
  >> "${LOG_FILE}" 2>&1 &
echo $! > "${PIDFILE}"

# 短暂等待进程启动
sleep 1
if ! kill -0 "$(cat "${PIDFILE}")" 2>/dev/null; then
  echo "[cmd/main] faststrm failed to start. tail of log:" >&2
  tail -n 40 "${LOG_FILE}" >&2 || true
  exit 1
fi
echo "[cmd/main] faststrm pid=$(cat "${PIDFILE}"), log=${LOG_FILE}" >&2
exit 0
FNOS_EOF

  # 给所有 cmd 脚本可执行权限
  chmod +x "${CMD_DIR}/"*

  # --- 4. 架构校验 ---
  echo "==> [${ARCH}] verify manifest"
  if [ "$ARCH" = "amd64" ]; then
    MANIFEST_ARCH="$(grep '^arch' "${MANIFEST}" | awk -F= '{gsub(/[[:space:]]/,"",$2); print $2}')"
    [ "${MANIFEST_ARCH}" = "x86_64" ] || { echo "MISMATCH: manifest arch=${MANIFEST_ARCH}, expected x86_64" >&2; exit 1; }
  else
    MANIFEST_PLATFORM="$(grep '^platform' "${MANIFEST}" | awk -F= '{gsub(/[[:space:]]/,"",$2); print $2}')"
    [ "${MANIFEST_PLATFORM}" = "arm" ] || { echo "MISMATCH: manifest platform=${MANIFEST_PLATFORM}, expected arm" >&2; exit 1; }
  fi

  # --- 5. fnpack 打包 ---
  echo "==> [${ARCH}] fnpack build"
  (cd "${STAGE}" && fnpack build)

  FPK="${STAGE}/faststrm.fpk"
  if [ ! -f "${FPK}" ]; then
    echo "ERROR: fnpack did not generate faststrm.fpk" >&2
    exit 1
  fi

  PKG_NAME="faststrm-${ARCH}-${VERSION}.fpk"
  PKG_PATH="${DIST_DIR}/${PKG_NAME}"
  cp "${FPK}" "${PKG_PATH}"
  rm -f "${FPK}"

  # 同时保留一份解压后的目录在 dist 下，方便用户手工查看/定制
  STAGE_COPY="${DIST_DIR}/faststrm-${ARCH}-${VERSION}"
  rm -rf "${STAGE_COPY}"
  cp -R "${STAGE}" "${STAGE_COPY}"
  # 清掉解压目录里的空目录占位，避免用户上传混淆
  echo "${VERSION}" > "${STAGE_COPY}/VERSION"

  SIZE="$(du -h "${PKG_PATH}" | cut -f1)"
  echo "    built -> ${PKG_PATH} (${SIZE})"
  echo "    stage copy -> ${STAGE_COPY}"
done

echo
echo "Done. Artifacts in: ${DIST_DIR}"
echo "  .fpk = 可上传到飞牛应用中心的安装包"
echo "  faststrm-{arch}-{VERSION}/ = 解压后的应用目录(可手工定制后再打包)"
