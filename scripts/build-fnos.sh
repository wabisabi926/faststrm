#!/usr/bin/env bash
# build-fnos.sh — 构建飞牛应用 .fpk 包
#
# 用法:
#   ./scripts/build-fnos.sh [amd64|arm64|all]
#
# 依赖:
#   - go (交叉编译 Linux 二进制)
#   - templ (生成模板代码，可选)
#   - fnpack (飞牛官方打包工具)
#       下载: https://static2.fnnas.com/fnpack/fnpack-1.2.1-linux-amd64
#       安装: chmod +x fnpack && sudo mv fnpack /usr/local/bin/
#
# 产物: dist/faststrm-{arch}-{version}.fpk
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

VERSION="$(grep '^version' deploy/fnos/faststrm-amd64/manifest 2>/dev/null | sed 's/.*=[[:space:]]*//' | tr -d '\r\n' || echo dev)"
DIST_DIR="${ROOT_DIR}/dist"
mkdir -p "${DIST_DIR}"

ARCHES=("${1:-amd64}")
if [ "${ARCHES[0]}" = "all" ]; then
  ARCHES=(amd64 arm64)
fi

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
  templ generate
fi

for ARCH in "${ARCHES[@]}"; do
  case "$ARCH" in
    amd64) GOARCH=amd64 ;;
    arm64) GOARCH=arm64 ;;
    *) echo "Unknown arch: $ARCH"; exit 1 ;;
  esac

  STAGE="${ROOT_DIR}/deploy/fnos/faststrm-${ARCH}"
  APP_DIR="${STAGE}/app"
  MANIFEST="${STAGE}/manifest"

  echo "==> [${ARCH}] building Go binary (GOOS=linux GOARCH=${GOARCH})"
  CGO_ENABLED=0 GOOS=linux GOARCH="${GOARCH}" \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
      -o "${APP_DIR}/faststrm" ./cmd/server/
  chmod +x "${APP_DIR}/faststrm"

  echo "==> [${ARCH}] verify manifest"
  if [ "$ARCH" = "amd64" ]; then
    MANIFEST_ARCH="$(grep '^arch' "${MANIFEST}" | awk -F= '{gsub(/[[:space:]]/,"",$2); print $2}')"
    [ "${MANIFEST_ARCH}" = "x86_64" ] || { echo "MISMATCH: manifest arch=${MANIFEST_ARCH}, expected x86_64" >&2; exit 1; }
  else
    MANIFEST_PLATFORM="$(grep '^platform' "${MANIFEST}" | awk -F= '{gsub(/[[:space:]]/,"",$2); print $2}')"
    [ "${MANIFEST_PLATFORM}" = "arm" ] || { echo "MISMATCH: manifest platform=${MANIFEST_PLATFORM}, expected arm" >&2; exit 1; }
  fi

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

  echo "    built -> ${PKG_PATH} ($(du -h "${PKG_PATH}" | cut -f1))"
done

echo
echo "Done. Artifacts in: ${DIST_DIR}"
