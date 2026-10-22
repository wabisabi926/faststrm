# 部署与运维

用户安装、Docker Compose、飞牛应用中心、首次启动向导请以 [GitHub Wiki](https://github.com/wabisabi926/faststrm/wiki) 为准。本目录只给维护者记录打包/运维注意事项。

| 文档 | 内容 |
|---|---|
| [fnos.md](fnos.md) | 飞牛 fpk 构建与版本覆盖 |
| [docker.md](docker.md) | 镜像/配置目录/端口 |
| [windows.md](windows.md) | Windows 测试包与托盘 |

通用端口：

- `8090`：FastStrm Web UI / `/api/strm`
- `8097`：Emby 反向代理（客户端必须连这个端口播放原盘）
- `8096`：Emby 本体（不经过 FastStrm 改写）

不要把 `8090` 直接暴露公网；`/api/strm` 是公开播放端点。
