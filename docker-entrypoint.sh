#!/bin/sh
set -e

# FastStrm Go 版入口脚本
# 说明：Go 后端 config.InitApp() 会在配置目录下自行创建 JSON 多文件配置：
#   config.json / account.json / tasks.json / settings.json
# 配置目录优先级（和 cmd/server/main.go getDefaultRoot 对齐）：
#   --config <DIR> flag > $DEFAULT_CONFIG_DIR > $CONFIG_DIR > 工作目录/.config > /app/.config
#
# 这里同时导出两个拼写版本，保证无论用户挂载哪一套环境变量都能生效。

CONFIG_DIR="${CONFIG_DIR:-/app/config}"
DATA_DIR="${DATA_DIR:-/app/data}"
# 双保险：DEFAULT_CONFIG_DIR 是 Go 后端原生识别的名字，CONFIG_DIR 是 docker/fNOS 通用名
export CONFIG_DIR
export DEFAULT_CONFIG_DIR="${DEFAULT_CONFIG_DIR:-${CONFIG_DIR}}"
export DATA_DIR

echo "==> FastStrm Go Server starting..."
echo "    CONFIG_DIR         (used by Docker / fNOS scripts) = ${CONFIG_DIR}"
echo "    DEFAULT_CONFIG_DIR (primary env for Go backend)    = ${DEFAULT_CONFIG_DIR}"
echo "    DATA_DIR                                            = ${DATA_DIR}"

exec "$@"
