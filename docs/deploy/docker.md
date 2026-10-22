# Docker

## 目录与端口

| 宿主机 | 容器 | 说明 |
|---|---|---|
| `<config>` | `/app/config` | config/account/tasks/settings（实际读写目录，`CONFIG_DIR`） |
| `<data>` | `/app/data` | SQLite（WAL）与运行数据 |
| `8090` | `8090` | Web UI + `/api/strm` |
| `8097` | `8097` | Emby 反向代理（可选） |

> `/app/.config` 只是镜像内置的**默认配置模板**目录（`DEFAULT_CONFIG_DIR`），不要挂载到它，
> 否则 `InitApp` 找不到模板；用户配置请挂 `/app/config`。

## 端口映射规则

反代端口的**容器侧**必须等于 UI 里的「反代监听端口」（`emby.proxyPort`，默认 8097）：

```yaml
ports:
  - "8090:8090"
  - "8097:8097"   # 容器侧 8097 = UI 的 proxyPort
  # 宿主机 8097 被占用时可以换宿主机侧： "18097:8097"
```

- 容器内监听的是 `settings.json` 里的 `emby.proxyPort`（UI：设置 → 通知中心 → Emby）。
- 改了 `proxyPort` 必须同步改映射的容器侧（例如 `"9000:9000"`），再 `docker compose up -d` 重建容器。
- 写成 `"8097:9000"` 这类容器侧错位的映射会导致客户端连 8097 无响应（容器内没有进程监听 9000）。
- 宿主机侧端口可以不同（`"18097:8097"`）：反代按请求的 `X-Forwarded-Port` / Host 端口改写
  `system/info`，客户端统一用宿主机端口访问即可。
- 不需要反代时可以不映射 8097，主功能不受影响。

## 容器内的地址填写

- **Emby 服务器地址**：不能用 `127.0.0.1`（那是容器自身）。Emby 在宿主机上用
  `http://<宿主机局域网IP>:8096`；Emby 在同一 compose 网络里用服务名 `http://emby:8096`。
- **STRM 前缀 `strmPrefix`**：用 `http://<宿主机IP>:8090`，保证 Emby 与播放器都能访问。
- 客户端连接反代时用 `http://<宿主机IP>:8097`，不要用容器名。

## 健康检查

`GET /api/health` 返回 `{"status":"ok","version":"..."}`。

## 首次启动

- 新安装：打开 `http://<host>:8090` 完成 Setup Wizard。
- 无界面部署：设置 `FASTSTRM_ADMIN_USERNAME` / `FASTSTRM_ADMIN_PASSWORD` 跳过向导。
- 已有安装：保持原密码，不需要重新初始化。

## 升级

保留 config/data 卷；升级后检查：
- 反代端口是否仍然监听（日志 `[EmbyProxy] 启动中: 0.0.0.0:8097`）；
- compose 里的反代端口映射是否仍与 `proxyPort` 一致；
- `/api/emby/proxy/status` 的 `clientAddr`；
- 旧 `/api/fs/get` STRM 是否需要迁移。
