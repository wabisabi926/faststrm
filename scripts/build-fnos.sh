#!/usr/bin/env bash
# build-fnos.sh — 构建飞牛 (fNOS) 应用 .fpk 包 (对齐 qmediasync FNOS 根目录模式)
#
# 用法:
#   ./scripts/build-fnos.sh [amd64|arm64|all]
#
# 依赖:
#   - go (交叉编译 Linux 二进制)
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

# 版本号优先取 git tag (vX.Y.Z -> X.Y.Z), 否则从 FNOS 下的 manifest 读
MANIFEST_VERSION="$(grep '^version' FNOS/faststrm-amd64/manifest 2>/dev/null | sed 's/.*=[[:space:]]*//' | tr -d '\r\n' || true)"
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
echo "    Template dir:  FNOS/faststrm-{amd64,arm64}/ (root level, aligned qmediasync)"

# 检查 fnpack
if ! command -v fnpack >/dev/null 2>&1; then
  echo "ERROR: fnpack not found in PATH." >&2
  echo "  Download: https://static2.fnnas.com/fnpack/fnpack-1.2.1-linux-amd64" >&2
  echo "  Install:  chmod +x fnpack && sudo mv fnpack /usr/local/bin/" >&2
  exit 1
fi

# 确保 Go 依赖就位（CI runner 是干净环境，显式 go mod download 避免 go build 隐式拉依赖失败直接炸 set -e）
echo "==> go mod download"
go mod download

# 确保前端构建产物已就位 (internal/web/spa) —— embed 嵌入不需要额外动作
if [ ! -f "${ROOT_DIR}/internal/web/spa/index.html" ]; then
  echo "WARN: internal/web/spa/index.html 未找到，请先构建前端 (cd frontend && npm i && npm run build)" >&2
  echo "      并把 frontend/dist/* 复制到 internal/web/spa/。否则二进制里没有 Web UI 页面。" >&2
fi

for ARCH in "${ARCHES[@]}"; do
  case "$ARCH" in
    amd64) GOARCH=amd64 ; EXPECT_PLATFORM="x86_64" ; FIELD_NAME="arch"     ;;
    arm64) GOARCH=arm64 ; EXPECT_PLATFORM="arm"    ; FIELD_NAME="platform" ;;
    *) echo "Unknown arch: $ARCH"; exit 1 ;;
  esac

  # ---- staging 目录用 mktemp -d，永不污染 git 管理的 FNOS/ ----
  # FNOS/faststrm-${ARCH}/ 是完整应用骨架 (manifest/config.yml/cmd/wizard/ICON*.PNG)
  TEMPLATE_SRC="${ROOT_DIR}/FNOS/faststrm-${ARCH}"
  if [ ! -d "${TEMPLATE_SRC}" ]; then
    echo "ERROR: template dir missing: ${TEMPLATE_SRC}" >&2
    exit 1
  fi

  STAGE="$(mktemp -d -t "faststrm-fnos-${ARCH}-XXXXXX")"
  trap 'rm -rf "${STAGE}"' RETURN
  echo "==> [${ARCH}] staging in tmpdir: ${STAGE}"

  # --- 1. 复制整个 FNOS 骨架到 staging (除了 app/ 空占位目录我们会自己填二进制) ---
  #     用 rsync -a 或 cp -R；过滤掉可能存在的临时文件
  # shellcheck disable=SC2199
  if [ -n "$(ls -A "${TEMPLATE_SRC}" 2>/dev/null)" ]; then
    cp -R "${TEMPLATE_SRC}/." "${STAGE}/"
  fi
  # 确保子目录都存在（cmd/wizard/app/ui/images，避免 git 空目录/大小写丢失问题）
  mkdir -p "${STAGE}/app" "${STAGE}/cmd" "${STAGE}/wizard" \
           "${STAGE}/app/ui" "${STAGE}/app/ui/images"

  # 调试：staging 骨架拷贝完就打一份目录清单（出问题时一眼看出 ICON.PNG/ui/ 是否到位）
  echo "==> [${ARCH}] staging contents after skeleton copy:"
  ls -la "${STAGE}" || true
  echo "--- app dir contents:"
  ls -la "${STAGE}/app" || true
  echo "--- app/ui dir contents (desktop_uidir check):"
  ls -la "${STAGE}/app/ui" || true

  MANIFEST="${STAGE}/manifest"
  APP_DIR="${STAGE}/app"
  CMD_DIR="${STAGE}/cmd"
  WIZARD_DIR="${STAGE}/wizard"
  chmod 755 "${APP_DIR}" "${CMD_DIR}" "${WIZARD_DIR}" 2>/dev/null || true

  # --- 1a. manifest 平台字段 & 版本号一致性校验（永远不回写 git 源文件） ---
  #     fNOS amd64 使用 `arch` 字段；arm64 使用 `platform` 字段（用户纠正）。
  #     qmediasync 里 amd64 写 `platform=x86_64` 是错的，不要照抄。
  if [ ! -f "${MANIFEST}" ]; then
    echo "ERROR: manifest missing at ${MANIFEST} (is FNOS skeleton complete?)" >&2
    exit 1
  fi
  ACTUAL_PLATFORM="$(grep "^${FIELD_NAME}" "${MANIFEST}" | awk -F= '{gsub(/[[:space:]]/,"",$2); print $2}')"
  if [ "${ACTUAL_PLATFORM}" != "${EXPECT_PLATFORM}" ]; then
    echo "MISMATCH: ${ARCH} manifest ${FIELD_NAME}=${ACTUAL_PLATFORM}, expected ${EXPECT_PLATFORM}" >&2
    exit 1
  fi
  # 在 staging manifest 中同步版本号（TAG 优先）
  TMP_MANIFEST="$(mktemp)"
  sed -E "s/^version[[:space:]]*=[[:space:]]*.*/version = ${VERSION}/" \
    "${MANIFEST}" > "${TMP_MANIFEST}" && mv "${TMP_MANIFEST}" "${MANIFEST}"
  chmod 644 "${MANIFEST}"

  # --- 1b. cmd/ 回调脚本权限统一 0755（fNOS 以应用身份运行，需要组执行位） ---
  #     FNOS 仓库中的 9 个脚本：main / install_init / install_callback /
  #     uninstall_init / uninstall_callback / upgrade_init / upgrade_callback /
  #     config_init / config_callback
  if [ -n "$(ls -A "${CMD_DIR}" 2>/dev/null)" ]; then
    chmod 755 "${CMD_DIR}"/* 2>/dev/null || true
    # 确保脚本使用 LF 换行符（避免 CRLF 导致 /bin/bash^M 找不到解释器）
    for SHFILE in "${CMD_DIR}"/*; do
      [ -f "${SHFILE}" ] || continue
      if command -v dos2unix >/dev/null 2>&1; then
        dos2unix -q "${SHFILE}" 2>/dev/null || true
      else
        sed -i 's/\r$//' "${SHFILE}" 2>/dev/null || true
      fi
    done
  else
    echo "WARN: ${CMD_DIR} is empty — fNOS callbacks won't fire. Add scripts under FNOS/faststrm-${ARCH}/cmd/." >&2
  fi

  # wizard 文件一般是 JSON，读权限即可：
  if [ -n "$(ls -A "${WIZARD_DIR}" 2>/dev/null)" ]; then
    find "${WIZARD_DIR}" -type f -exec chmod 644 {} +
    find "${WIZARD_DIR}" -type d -exec chmod 755 {} +
  fi

  # --- 2. 交叉编译 Go 二进制 (Linux) + 填 app/ 目录 ---
  echo "==> [${ARCH}] building Go binary (GOOS=linux GOARCH=${GOARCH})"
  CGO_ENABLED=0 GOOS=linux GOARCH="${GOARCH}" \
    go build -trimpath \
      -ldflags="-s -w \
        -X 'github.com/wabisabi926/faststrm/internal/handler.appVersion=v${VERSION}' \
        -X 'github.com/wabisabi926/faststrm/internal/web.appVersion=v${VERSION}' \
        -X 'main.version=v${VERSION}' \
        -X 'main.BuildDate=${BUILD_DATE}'" \
      -o "${APP_DIR}/faststrm" ./cmd/server/
  chmod 755 "${APP_DIR}/faststrm"

  # 拷贝默认配置模板 & docker entrypoint（与容器镜像保持一致）
  if [ -d "${ROOT_DIR}/.config" ]; then
    echo "==> [${ARCH}] copy default config templates -> app/.config"
    cp -R "${ROOT_DIR}/.config" "${APP_DIR}/.config"
    find "${APP_DIR}/.config" -type d -exec chmod 755 {} +
    find "${APP_DIR}/.config" -type f -exec chmod 644 {} +
  fi
  if [ -f "${ROOT_DIR}/docker-entrypoint.sh" ]; then
    cp    "${ROOT_DIR}/docker-entrypoint.sh" "${APP_DIR}/entrypoint.sh"
    chmod 755 "${APP_DIR}/entrypoint.sh"
    # 同样 LF 换行符清洗
    if command -v dos2unix >/dev/null 2>&1; then dos2unix -q "${APP_DIR}/entrypoint.sh" 2>/dev/null || true
    else sed -i 's/\r$//' "${APP_DIR}/entrypoint.sh" 2>/dev/null || true ; fi
  fi

  # --- 3. fnpack 打包（对齐 qmediasync：cd 到 staging 目录直接 fnpack build） ---
  # 打包前再打一次 staging 目录清单 + 二进制存在性确认，防止 CI 上 exit 1 时看不到原因
  echo "==> [${ARCH}] staging contents right before fnpack build:"
  ls -la "${STAGE}" || true
  ls -la "${STAGE}/app" || true
  [ -f "${STAGE}/app/faststrm" ] && echo "    app/faststrm binary size: $(du -h "${STAGE}/app/faststrm" | cut -f1)" || echo "    !! app/faststrm binary MISSING"

  echo "==> [${ARCH}] fnpack build in ${STAGE}"
  (cd "${STAGE}" && fnpack build 2>&1)
  FNPACK_RC=$?
  if [ "${FNPACK_RC}" -ne 0 ]; then
    echo "ERROR: fnpack build exited with code ${FNPACK_RC}. Dumping last 40 lines of staging contents + nested:" >&2
    ls -laR "${STAGE}" >&2 || true
    exit "${FNPACK_RC}"
  fi

  FPK="${STAGE}/faststrm.fpk"
  if [ ! -f "${FPK}" ]; then
    # fnpack 在部分版本里生成的包名是 {appname}_{version}.fpk，fallback 找一下
    ALT_FPK="$(find "${STAGE}" -maxdepth 1 -type f -name '*.fpk' | head -n1)"
    if [ -n "${ALT_FPK}" ] && [ -f "${ALT_FPK}" ]; then
      FPK="${ALT_FPK}"
      echo "    (fnpack produced ${FPK##*/} instead of faststrm.fpk, using it)"
    else
      echo "ERROR: fnpack did not produce any .fpk in ${STAGE}" >&2
      echo "  staging contents:" >&2
      ls -la "${STAGE}" >&2 || true
      exit 1
    fi
  fi

  PKG_NAME="faststrm-${ARCH}-${VERSION}.fpk"
  PKG_PATH="${DIST_DIR}/${PKG_NAME}"
  cp "${FPK}" "${PKG_PATH}"

  # 同时保留一份解压后的目录在 dist 下，方便用户手工查看/定制
  STAGE_COPY="${DIST_DIR}/faststrm-${ARCH}-${VERSION}"
  rm -rf "${STAGE_COPY}"
  cp -R "${STAGE}" "${STAGE_COPY}"
  find "${STAGE_COPY}" -type d -exec chmod 755 {} +
  find "${STAGE_COPY}" -type f -exec chmod u+rw,go+r {} +
  if [ -d "${STAGE_COPY}/cmd" ]; then
    chmod 755 "${STAGE_COPY}/cmd"/* 2>/dev/null || true
  fi
  [ -f "${STAGE_COPY}/app/entrypoint.sh" ] && chmod 755 "${STAGE_COPY}/app/entrypoint.sh" || true
  [ -f "${STAGE_COPY}/app/faststrm"      ] && chmod 755 "${STAGE_COPY}/app/faststrm"      || true
  echo "${VERSION}" > "${STAGE_COPY}/VERSION"
  chmod 644 "${STAGE_COPY}/VERSION" "${STAGE_COPY}/manifest" 2>/dev/null || true

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
