# Docker

## 目录与端口

| 宿主机 | 容器 | 说明 |
|---|---|---|
| `<config>` | `/app/.config` | config/account/tasks/settings |
| `<data>` | `/app/data` | SQLite（WAL） |
| `8090` | `8090` | Web UI + `/api/strm` |
| `8097` | `8097` | Emby 反向代理 |

## 健康检查

`GET /api/health` 返回 `{"status":"ok","version":"..."}`。

## 首次启动

- 新安装：打开 `http://<host>:8090` 完成 Setup Wizard。
- 无界面部署：设置 `FASTSTRM_ADMIN_USERNAME` / `FASTSTRM_ADMIN_PASSWORD` 跳过向导。
- 已有安装：保持原密码，不需要重新初始化。

## 升级

保留 config/data 卷；升级后检查：
- 反代端口是否仍然监听；
- `/api/emby/proxy/status` 的 `clientAddr`；
- 旧 `/api/fs/get` STRM 是否需要迁移。
