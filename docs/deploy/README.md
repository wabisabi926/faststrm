# 部署与运维

用户安装、Docker Compose、飞牛应用中心、首次启动向导请以 [GitHub Wiki](https://github.com/wabisabi926/faststrm/wiki) 为准。本目录只给维护者记录打包/运维注意事项。

| 文档 | 内容 |
|---|---|
| [fnos.md](fnos.md) | 飞牛 fpk 构建与版本覆盖 |
| [docker.md](docker.md) | 镜像/配置目录/端口 |
| [windows.md](windows.md) | Windows 测试包与托盘 |

通用端口：

- `8090`：FastStrm Web UI / `/api/strm`
- `8097`：Emby 反向代理（Docker 需额外映射，容器侧端口与 UI 的 `proxyPort` 一致；客户端必须连这个端口播放原盘）
- `8096`：Emby 本体（不经过 FastStrm 改写）

不要把 `8090` 直接暴露公网；`/api/strm` 是公开播放端点。

## 运行环境变量

| 变量 | 说明 |
|---|---|
| `FASTSTRM_LOG_LEVEL` | 日志级别：`debug` / `info` / `warn` / `error`，默认 `info`；非法值回退 `info` |
| `FASTSTRM_ADMIN_USERNAME` / `FASTSTRM_ADMIN_PASSWORD` | 无界面部署首次初始化管理员，跳过 Web 向导 |
| `CONFIG_DIR` / `DEFAULT_CONFIG_DIR` | 实际配置目录 / 镜像内置模板目录 |
| `FASTSTRM_VERSION`（构建时） | fnOS fpk 等未打 tag 构建的版本覆盖 |

## 优雅退出

收到 SIGINT/SIGTERM 或托盘退出时，服务先停止调度、监控和反代，再取消并等待运行中任务退出，最长 20s。Docker 默认 10s 后 SIGKILL，会截断排空；使用 Docker 时请把 `stop_grace_period` 设为至少 `25s`。
