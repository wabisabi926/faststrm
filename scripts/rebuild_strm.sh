#!/bin/bash
# ============================================================
# STRM URL 重建脚本  —— /api/fs/get → /api/strm
# 适用：飞牛 NAS / Linux 环境
# 用法：
#   ./rebuild_strm.sh                      # 干跑预览
#   ./rebuild_strm.sh /mnt/user/strms      # 指定目录
#   ./rebuild_strm.sh /mnt/user/strms -w   # 实际写入
# ============================================================

set -euo pipefail

DRY_RUN=true
TARGET_DIR=""

# 解析参数
for arg in "$@"; do
    case "$arg" in
        -w|--write)   DRY_RUN=false ;;
        -h|--help)    sed -n '2,15p' "$0"; exit 0 ;;
        *)            TARGET_DIR="$arg" ;;
    esac
done

# 默认路径
if [ -z "$TARGET_DIR" ]; then
    for cand in "./data/strm" "/mnt/user/strm" "/mnt/user/appdata/faststrm/strm"; do
        if [ -d "$cand" ]; then
            TARGET_DIR="$cand"
            break
        fi
    done
fi

if [ -z "$TARGET_DIR" ] || [ ! -d "$TARGET_DIR" ]; then
    echo "[ERROR] 目录不存在: ${TARGET_DIR:-<未指定>}"
    exit 1
fi

TOTAL=0
CHANGED=0
NOCHANGE=0
ERRORS=0

echo ""
echo "============================================"
echo "  STRM URL 重建脚本  ( /api/fs/get → /api/strm )"
echo "============================================"
echo "  扫描目录 : $TARGET_DIR"
echo "  实际写入 : $([ "$DRY_RUN" = true ] && echo 'NO (干跑预览)' || echo 'YES')"
echo "============================================"
echo ""

# 遍历所有 .strm 文件
while IFS= read -r -d '' file; do
    TOTAL=$((TOTAL + 1))
    rel="${file#$TARGET_DIR/}"

    old_content=$(cat "$file" 2>/dev/null) || {
        echo "  [ERR] $rel — 读取失败"
        ERRORS=$((ERRORS + 1))
        continue
    }

    # 替换：/api/fs/get → /api/strm（保留查询参数）
    new_content=$(printf '%s' "$old_content" | sed -E 's|/api/fs/get(\?|$)|/api/strm\1|g; s|/api/fs[_-]?get(\?|$)|/api/strm\1|g')

    if [ "$new_content" = "$old_content" ]; then
        NOCHANGE=$((NOCHANGE + 1))
        continue
    fi

    old_line=$(printf '%s' "$old_content" | head -1)
    new_line=$(printf '%s' "$new_content" | head -1)

    if [ "$DRY_RUN" = true ]; then
        echo "  [DRY] $rel"
        echo "       - OLD: $old_line"
        echo "       + NEW: $new_line"
    else
        # 保留原文件权限，用临时文件安全替换
        tmp="${file}.tmp"
        printf '%s' "$new_content" > "$tmp" && mv "$tmp" "$file"
        echo "  [OK]  $rel"
        echo "       - OLD: $old_line"
        echo "       + NEW: $new_line"
    fi
    CHANGED=$((CHANGED + 1))
done < <(find "$TARGET_DIR" -type f -name "*.strm" -print0)

echo ""
echo "============================================"
echo "  汇总"
echo "============================================"
echo "  扫描文件  : $TOTAL"
echo "  已更新    : $CHANGED"
echo "  无变化    : $NOCHANGE"
echo "  错误      : $ERRORS"
echo "============================================"

if [ "$DRY_RUN" = true ] && [ $CHANGED -gt 0 ]; then
    echo ""
    echo "提示：加 -w 参数即可实际写入："
    echo "      $0 $TARGET_DIR -w"
fi

if [ $CHANGED -gt 0 ] && [ "$DRY_RUN" = false ]; then
    echo ""
    echo "✅ 完成！建议重启 FastStrm 服务。"
fi
