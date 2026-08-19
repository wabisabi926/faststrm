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

# --- 清理历史 deploy/fnos 下的构建缓存 (旧脚本遗留产物) ---
# 新脚本用临时 staging 目录，不再污染 git 管理的 deploy/fnos/
# 这里清理一次旧的残留，避免用户 git status 看到大量 Ignored 未追踪
for OLD_ARCH in amd64 arm64; do
  OLD_BASE="deploy/fnos/faststrm-${OLD_ARCH}"
  [ -d "${OLD_BASE}" ] || continue
  # 只清 .gitignore 里忽略的子目录/文件，不动 manifest
  [ -d "${OLD_BASE}/cmd" ] && rm -rf "${OLD_BASE}/cmd" || true
  [ -d "${OLD_BASE}/app" ] && rm -rf "${OLD_BASE}/app" || true
  [ -f "${OLD_BASE}/faststrm.fpk" ] && rm -f "${OLD_BASE}/faststrm.fpk" || true
  [ -f "${OLD_BASE}/VERSION" ] && rm -f "${OLD_BASE}/VERSION" || true
done

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

  # ---- 关键权限修复 1: staging 目录用 mktemp -d，永不污染 git 管理的 deploy/fnos/ ----
  # 从 deploy/fnos/faststrm-${ARCH} 拷贝 *只有 manifest* 到临时 staging
  # 然后所有 app / cmd / 构建产物全部放在临时目录，打包后直接移到 dist
  TEMPLATE_SRC="${ROOT_DIR}/deploy/fnos/faststrm-${ARCH}"
  STAGE="$(mktemp -d -t "faststrm-fnos-${ARCH}-XXXXXX")"
  # 退出时清理临时 staging
  trap 'rm -rf "${STAGE}"' RETURN
  echo "==> [${ARCH}] staging in tmpdir: ${STAGE}"

  MANIFEST_SRC="${TEMPLATE_SRC}/manifest"
  MANIFEST="${STAGE}/manifest"
  APP_DIR="${STAGE}/app"
  CMD_DIR="${STAGE}/cmd"

  mkdir -p "${APP_DIR}" "${CMD_DIR}"
  cp "${MANIFEST_SRC}" "${MANIFEST}"
  chmod 644 "${MANIFEST}"

  # --- 1. 在临时 staging 内同步 manifest 版本号 (永远不会回写 git 源文件) ---
  TMP_MANIFEST="$(mktemp)"
  sed -E "s/^version[[:space:]]*=[[:space:]]*.*/version = ${VERSION}/" \
    "${MANIFEST}" > "${TMP_MANIFEST}" && mv "${TMP_MANIFEST}" "${MANIFEST}"
  chmod 644 "${MANIFEST}"

  echo "==> [${ARCH}] building Go binary (GOOS=linux GOARCH=${GOARCH})"
  CGO_ENABLED=0 GOOS=linux GOARCH="${GOARCH}" \
    go build -trimpath \
      -ldflags="-s -w \
        -X 'github.com/wabisabi926/faststrm/internal/handler.appVersion=v${VERSION}' \
        -X 'main.version=v${VERSION}' \
        -X 'main.BuildDate=${BUILD_DATE}'" \
      -o "${APP_DIR}/faststrm" ./cmd/server/
  chmod 755 "${APP_DIR}/faststrm"

  # --- 2. 拷贝默认配置模板 & entrypoint 到 app 目录 (与 Docker 镜像保持一致) ---
  echo "==> [${ARCH}] copy default config templates & entrypoint"
  cp -R "${ROOT_DIR}/.config"                "${APP_DIR}/.config"
  # 模板目录权限统一：目录 755 / 文件 644，避免打包机 umask 导致 fNOS 侧读不到
  find "${APP_DIR}/.config" -type d -exec chmod 755 {} +
  find "${APP_DIR}/.config" -type f -exec chmod 644 {} +
  cp    "${ROOT_DIR}/docker-entrypoint.sh"    "${APP_DIR}/entrypoint.sh"
  chmod 755 "${APP_DIR}/entrypoint.sh"

  # --- 3. 生成飞牛回调脚本 (cmd/main, install_callback, 等) ---
  echo "==> [${ARCH}] generate fNOS command scripts"

  # ---- 把"选择可写目录"的公共逻辑抽成一个 shell 函数嵌入所有脚本 ----
  # 三级 fallback:
  #   1) fNOS 透传的 FNOS_APP_* 环境变量 (highest priority)
  #   2) APP_ROOT 下对应的传统子目录 (如果 APP_ROOT 可写)
  #   3) /tmp/faststrm-<uid>/... (兜底，仅用于进程还能跑起来并提醒用户)
  _WRITABLE_BASE_FN='
__fn_writeable_dir() {
  # $1 = FNOS env var name; $2 = APP_ROOT 相对 fallback; $3 = 人类可读的用途名
  local want="$1" fallback="$2" usage="$3"
  local from_env=""
  eval "from_env=\"\${${want}:-}\""
  if [ -n "${from_env}" ]; then
    mkdir -p "${from_env}" 2>/dev/null || true
    if [ -d "${from_env}" ] && [ -w "${from_env}" ]; then
      printf "%s" "${from_env}"
      return 0
    fi
    printf "[WARN] %s=%s is not writable, falling back.\\n" "${want}" "${from_env}" >&2
  fi
  local under_app="${APP_ROOT}/${fallback}"
  mkdir -p "${under_app}" 2>/dev/null || true
  if [ -d "${under_app}" ] && [ -w "${under_app}" ]; then
    printf "%s" "${under_app}"
    return 0
  fi
  local safe_name="faststrm-$(id -u 2>/dev/null || echo 0)"
  local last="/tmp/${safe_name}/${fallback}"
  mkdir -p "${last}" 2>/dev/null || true
  if [ -d "${last}" ] && [ -w "${last}" ]; then
    printf "[WARN] ${usage} falling back to temp dir: %s (配置/数据不会持久化，请在 manifest 里声明 service_config_dir/service_data_dir 并让 fNOS 透传 %s 环境变量).\\n" "${last}" "${want}" >&2
    printf "%s" "${last}"
    return 0
  fi
  printf "ERROR: cannot find a writable dir for %s. tried: env %s, %s, %s\\n" "${usage}" "${want}" "${under_app}" "${last}" >&2
  return 1
}
'

  # cmd/install_callback — 安装后回调: 保证持久目录存在 (无实际动作，Go 程序会自建)
  {
    cat <<'FNOS_SCRIPT'
#!/bin/sh
# fNOS 会在应用包安装完成后调用
set -e
APP_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
FNOS_SCRIPT
    # 注入 __fn_writeable_dir 公共函数
    printf '%s\n' "${_WRITABLE_BASE_FN}"
    cat <<'FNOS_SCRIPT'
LOG_DIR="$(__fn_writeable_dir FNOS_APP_LOG_DIR logs logs)"
mkdir -p "${LOG_DIR}"
chmod 755 "${LOG_DIR}" 2>/dev/null || true
echo "[install_callback] faststrm installed at ${APP_ROOT}, log_dir=${LOG_DIR}" >&2
exit 0
FNOS_SCRIPT
  } > "${CMD_DIR}/install_callback"

  # cmd/uninstall_init — 卸载前回调: 先停进程
  {
    cat <<'FNOS_SCRIPT'
#!/bin/sh
set -e
APP_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
FNOS_SCRIPT
    printf '%s\n' "${_WRITABLE_BASE_FN}"
    cat <<'FNOS_SCRIPT'
RUN_DIR="$(__fn_writeable_dir FNOS_APP_RUN_DIR run run-pid)"
PIDFILE="${RUN_DIR}/faststrm.pid"
if [ -f "${PIDFILE}" ]; then
  PID="$(cat "${PIDFILE}" 2>/dev/null || true)"
  if [ -n "${PID}" ] && kill -0 "${PID}" 2>/dev/null; then
    kill -TERM "${PID}" 2>/dev/null || true
    sleep 1
    kill -KILL "${PID}" 2>/dev/null || true
  fi
  rm -f "${PIDFILE}" 2>/dev/null || true
fi
# 兜底：按进程名再杀一轮 (只杀本用户可见的进程，不依赖 root)
pkill -f "${APP_ROOT}/app/faststrm" 2>/dev/null || true
echo "[uninstall_init] faststrm stopped" >&2
exit 0
FNOS_SCRIPT
  } > "${CMD_DIR}/uninstall_init"

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
  # 注意：此脚本会被 fNOS 以非 root 的应用用户运行，APP_ROOT 可能是 squashfs 只读。
  #       所有需要写的路径必须经过 __fn_writeable_dir 三级 fallback 判定。
  {
    cat <<'FNOS_SCRIPT'
#!/bin/sh
# fNOS 应用启动入口。manifest.desktop_applaunchname / 服务模式都会调用此脚本。
set -eu
APP_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP_DIR="${APP_ROOT}/app"
FNOS_SCRIPT
    printf '%s\n' "${_WRITABLE_BASE_FN}"
    cat <<'FNOS_SCRIPT'

# ---- 每个写路径都走三级 fallback ----
LOG_DIR="$(__fn_writeable_dir FNOS_APP_LOG_DIR logs   logs)"
RUN_DIR="$(__fn_writeable_dir FNOS_APP_RUN_DIR run    run-pid)"
# CONFIG_DIR: 必须是 fNOS 能保留用户数据的持久目录
CONFIG_DIR="$(__fn_writeable_dir FNOS_APP_CONFIG_DIR config config)"
# DATA_DIR  : STRM 导出 / SQLite / JWT secret / 密码 salt 等
DATA_DIR="$(__fn_writeable_dir   FNOS_APP_DATA_DIR   data   data)"

mkdir -p "${LOG_DIR}" "${RUN_DIR}" "${CONFIG_DIR}" "${DATA_DIR}"
chmod 755 "${LOG_DIR}" "${RUN_DIR}" "${CONFIG_DIR}" "${DATA_DIR}" 2>/dev/null || true

PIDFILE="${RUN_DIR}/faststrm.pid"
LOG_FILE="${LOG_DIR}/faststrm.log"
touch "${LOG_FILE}" 2>/dev/null || true   # 如果 touch 失败，下面 nohup 重定向也会失败；但我们先尝试
chmod 644 "${LOG_FILE}" 2>/dev/null || true

# 如果上一次启动还在跑，先停掉
if [ -f "${PIDFILE}" ]; then
  OLD_PID="$(cat "${PIDFILE}" 2>/dev/null || true)"
  if [ -n "${OLD_PID}" ] && kill -0 "${OLD_PID}" 2>/dev/null; then
    kill -TERM "${OLD_PID}" 2>/dev/null || true
    sleep 1
  fi
  rm -f "${PIDFILE}" 2>/dev/null || true
fi
# 兜底：按路径精确匹配自己的那个 faststrm，避免误杀其他应用
pkill -f "${APP_DIR}/faststrm" 2>/dev/null || true
sleep 0.5

export TZ="${TZ:-Asia/Shanghai}"
# ---- 同时设置 CONFIG_DIR / DATA_DIR (entrypoint.sh 认) +
#                      DEFAULT_CONFIG_DIR / APP_DATA_DIR (Go InitApp 认) ----
export CONFIG_DIR="${CONFIG_DIR}"
export DATA_DIR="${DATA_DIR}"
export DEFAULT_CONFIG_DIR="${CONFIG_DIR}"
export APP_DATA_DIR="${DATA_DIR}"
export FNOS_APP_LOG_DIR="${LOG_DIR}"
export FNOS_APP_RUN_DIR="${RUN_DIR}"

cd "${APP_DIR}"
# nohup 一定要保证 LOG_FILE 可写，否则 nohup 会尝试写 nohup.out，而 APP_DIR 可能只读。
nohup \
  "${APP_DIR}/entrypoint.sh" "${APP_DIR}/faststrm" \
  >> "${LOG_FILE}" 2>&1 &
PID=$!
echo "${PID}" > "${PIDFILE}"
chmod 644 "${PIDFILE}" 2>/dev/null || true

# 短暂等待进程启动 (set +e，避免各种 edge 情况下检查命令把整个启动脚本杀崩)
set +e
sleep 2
START_OK=0
if [ -s "${PIDFILE}" ]; then
  CHECK_PID="$(cat "${PIDFILE}" 2>/dev/null || true)"
  if [ -n "${CHECK_PID}" ] && kill -0 "${CHECK_PID}" 2>/dev/null; then
    START_OK=1
  fi
fi
if [ "${START_OK}" -ne 1 ]; then
  {
    echo "[cmd/main] faststrm failed to start within 2s."
    echo "  pidfile=${PIDFILE}  content=[$(cat "${PIDFILE}" 2>/dev/null || echo EMPTY)]"
    echo "  log_file=${LOG_FILE}  last 60 lines:"
    tail -n 60 "${LOG_FILE}" 2>/dev/null || echo "  (cannot read log file)"
    echo "  runtime dirs:"
    for d in "${LOG_DIR}" "${RUN_DIR}" "${CONFIG_DIR}" "${DATA_DIR}"; do
      if [ -d "$d" ]; then
        printf "    %s  writable=%s\n" "$d" "$([ -w "$d" ] && echo yes || echo NO)"
      else
        printf "    %s  MISSING\n" "$d"
      fi
    done
  } >&2
  exit 1
fi
echo "[cmd/main] faststrm pid=$(cat "${PIDFILE}"), log=${LOG_FILE}" >&2
exit 0
FNOS_SCRIPT
  } > "${CMD_DIR}/main"

  # 给所有 cmd 脚本可执行权限 (0755 确保组/其他也能执行，兼容飞牛运行时切换身份)
  chmod 755 "${CMD_DIR}/"*

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

  # 同时保留一份解压后的目录在 dist 下，方便用户手工查看/定制
  STAGE_COPY="${DIST_DIR}/faststrm-${ARCH}-${VERSION}"
  rm -rf "${STAGE_COPY}"
  cp -R "${STAGE}" "${STAGE_COPY}"
  # stage copy 里的权限再刷一遍，确保 zip 之后解压的用户也能直接执行
  find "${STAGE_COPY}" -type d -exec chmod 755 {} +
  find "${STAGE_COPY}" -type f -exec chmod u+rw,go+r {} +
  chmod 755 "${STAGE_COPY}/cmd"/* "${STAGE_COPY}/app/entrypoint.sh" "${STAGE_COPY}/app/faststrm"
  echo "${VERSION}" > "${STAGE_COPY}/VERSION"
  chmod 644 "${STAGE_COPY}/VERSION" "${STAGE_COPY}/manifest"

  SIZE="$(du -h "${PKG_PATH}" | cut -f1)"
  echo "    built -> ${PKG_PATH} (${SIZE})"
  echo "    stage copy -> ${STAGE_COPY}"

  # 清 trap，手动删 tmp staging (避免下次迭代 trap 冲突)
  trap - RETURN
  rm -rf "${STAGE}"
done

echo
echo "Done. Artifacts in: ${DIST_DIR}"
echo "  .fpk = 可上传到飞牛应用中心的安装包"
echo "  faststrm-{arch}-{VERSION}/ = 解压后的应用目录(可手工定制后再打包)"
