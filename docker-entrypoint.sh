#!/bin/sh
set -e

# FastStrm Go 版入口脚本
# 初始化逻辑全部由 Go 后端的 config.InitApp() 处理
# 包括：目录创建、默认配置拷贝、admin密码哈希、internalToken生成

CONFIG_DIR="${CONFIG_DIR:-/app/config}"
DATA_DIR="${DATA_DIR:-/app/data}"

echo "==> FastStrm Go Server starting..."
echo "    config_dir: $CONFIG_DIR"
echo "    data_dir:   $DATA_DIR"

exec "$@"
