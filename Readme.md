<div align="center">
  <img src="https://raw.githubusercontent.com/wabisabi926/faststrm/refs/heads/go/frontend/public/logo.png" alt="Fast Strm Logo" width="200" height="200">
</div>

# Fast Strm

<div align="left">

**FastStrm — 让 115 网盘和你的播放器真正「同步」**

> 想象一下：你在 115 网盘存了一部电影，打开 Emby/Kodi 就能直接看；你在网盘删了，播放器里自动消失——FastStrm 就是实现这件事的工具。
>
> ✅ **播放器里看起来就是本地文件** — 海报、评分、收藏、播放进度全部正常，刮削一次搞定
>
> ✅ **不下载任何东西** — 播放时直连 115 CDN 流式传输，4K 秒开、不占硬盘
>
> ✅ **实时同步，全自动** — 10 秒感知网盘增删改，新上传的电影立刻生成 `.strm` 出现在播放器里
>
> ✅ **扫码登录，零配置** — 手机扫一扫就搞定，不用 F12 抓 Cookie，过期了再扫一下就好
>
> ✅ **异常主动通知** — Cookie 过期、账号异常，Telegram 第一时间推送，不用自己盯着看
>
> ✅ **Go 单二进制** — 纯 Go 架构，部署只需一个文件，内存占用低，启动快

</div>

---

## 🚀 核心特性

| 图标 | 特性 | 说明 |
|:----:|------|------|
| 🛰️ | **智能路由** | 该代理的代理，该直连的直连。Infuse 拖动进度条不卡，Emby/Kodi 零服务器带宽消耗；115 CDN 临时挂了自动降级 |
| 📡 | **实时监控** | 10 秒感知网盘变化，新上传自动生成 `.strm`、删除自动清理、重命名自动重建；单次大量删除自动熔断防误清 |
| 📺 | **Emby 深度集成** | 双向打通：Emby 里删片自动清 `.strm`（三层保险防误删），入库等刮削完才通知；**Emby for Kodi ISO 原盘播放修复**（反向代理强制 DirectPlay） |
| 📱 | **扫码登录** | 手机 App 扫码自动获取 Cookie，无需 F12 抓包；失效后一键扫码刷新 |
| 💬 | **Telegram 通知** | 任务进度、Emby 入库/删片、账号 Cookie 过期/恢复——家里有动静，手机立刻知道；多集更新自动合并不刷屏 |
| 🧾 | **任务历史** | 每次全量扫描 / 手动执行完整记录，可按任务名筛选；执行中实时日志推送到前端 |
| 🔐 | **防盗链保护** | 开启后播放链接带动态短时口令，被偷走也没用，防止别人用你的账号消耗流量和并发 |
| 🏗️ | **Go 原生架构** | Go + go-zero 单二进制，前端 SPA 通过 `go:embed` 嵌入，零运行时依赖 |

---

## 💬 交流社群

Telegram 用户群：[t.me/+J6csNlBG6q1iYjBl](https://t.me/+J6csNlBG6q1iYjBl) · 讨论使用问题、反馈建议、获取更新推送

## 📦 快速开始

```bash
# 克隆（go 分支为准）
git clone -b go https://github.com/wabisabi926/faststrm.git
cd faststrm
docker-compose up -d

# 或使用 Docker 镜像
docker pull wabisabi926/faststrm:latest
docker run -d \
  --name faststrm \
  -p 8090:8090 \
  -v ./config:/app/config \
  -v ./data:/app/data \
  -v /path/to/your/strm:/app/data/strm \
  -e TZ=Asia/Shanghai \
  -e APP_ENV=prod \
  --restart unless-stopped \
  wabisabi926/faststrm:latest
```

启动后访问 `http://localhost:8090`，默认账号密码：**admin / admin**

> ⚠️ 首次登录请立即修改默认密码！

---

## 🛠️ 下载 / 安装方式

FastStrm 提供 3 种官方分发方式，任选其一：

| 方式 | 适用场景 | 下载地址 / 命令 |
|:----:|----------|-----------------|
| 🐳 **Docker**（最通用） | Linux / NAS / macOS / Windows，已有 Docker 环境 | `docker pull wabisabi926/faststrm:latest` |
| 🐂 **飞牛 fNOS .fpk** | 飞牛 NAS（X86/ARM 机型），一键手动安装 | [GitHub Releases → 选择 `faststrm-{amd64\|arm64}-1.0.0.fpk`](https://github.com/wabisabi926/faststrm/releases) |
| 🖥️ **源码 / 单二进制** | 想自己编译或跑在普通 Linux 主机 | `git clone -b go https://github.com/wabisabi926/faststrm && cd faststrm && go build ./cmd/server/` |

> 📘 飞牛打包、定制、运行目录和排错详见 [docs/飞牛打包部署.md](docs/飞牛打包部署.md)

---

## 🏗️ 架构说明

FastStrm v1.1.3 采用纯 Go 架构：

```
┌─────────────────────────────────────────────────┐
│              FastStrm (Go 单二进制)               │
│  ┌───────────────┐  ┌─────────────────────────┐  │
│  │  go-zero HTTP  │  │  embed.FS (前端 SPA)   │  │
│  │  REST API      │  │  Vite + React 构建产物 │  │
│  │  + Swagger UI  │  │                         │  │
│  └───────────────┘  └─────────────────────────┘  │
│  ┌─────────────────────────────────────────────┐  │
│  │  SQLite (嵌入式) + 版本化迁移框架           │  │
│  │  STRM URL 签名（可选，防盗链）              │  │
│  └─────────────────────────────────────────────┘  │
│         ↑ 都由 Go 进程直接处理，无需 Nginx         │
└─────────────────────────────────────────────────┘
         ↑
    单端口: 8090
    单二进制: faststrm
```

- **后端**：Go + go-zero，纯 HTTP API，内置 Swagger UI（`/api/docs/ui`）
- **前端**：Vite + React SPA，通过 `//go:embed` 嵌入二进制
- **数据库**：SQLite（嵌入式）+ 自研版本化迁移框架（事务化、幂等）
- **配置存储**：JSON 文件加密存储（settings.json 自动迁移补齐新字段）
- **STRM 链接保护**：可选 URL 签名防盗链，开启后播放链接带动态短时口令，被偷走也没用；默认关闭，向后兼容老 STRM
- **部署**：一个 `faststrm` 二进制 + 配置目录，无需 Node.js、Nginx、PM2

---

## 🔗 详细文档

> **完整配置说明、功能详解、路由策略、排错指南请查看 [GitHub Wiki](https://github.com/wabisabi926/faststrm/wiki)**

| 主题 | 说明 |
|------|------|
| [🚀 快速开始](https://github.com/wabisabi926/faststrm/wiki/快速开始) | 首次使用全流程 |
| [⚙️ 配置说明](https://github.com/wabisabi926/faststrm/wiki/配置说明) | settings.json 全字段参考 |
| [🔀 STRM 路由策略](https://github.com/wabisabi926/faststrm/wiki/STRM-路由策略) | 302 vs 代理决策逻辑 |
| [🗑️ 删除同步](https://github.com/wabisabi926/faststrm/wiki/删除同步) | Emby 删除事件自动清理 |
| [📝 版本日志](https://github.com/wabisabi926/faststrm/wiki/版本更新日志) | 历史版本更新记录 |

---

## 📄 许可证

本项目采用 [MIT License](LICENSE) 许可证。

## ⚠️ 免责声明

本项目仅供学习和研究使用。请确保你遵守相关的法律法规和服务条款。
