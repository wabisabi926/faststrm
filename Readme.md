<div align="center">
  <img src="https://raw.githubusercontent.com/wabisabi926/faststrm/refs/heads/go/frontend/public/logo.png" alt="Fast Strm Logo" width="200" height="200">
</div>

# Fast Strm

<div align="left">

**FastStrm — 让 115 网盘和你的播放器真正「同步」**

> 想象一下：你在 115 网盘存了一部电影，打开 Emby/Kodi 就能直接看；你在网盘删了，播放器里自动消失——FastStrm 就是实现这件事的工具。
>
> ✅ **网盘增删改，本地秒同步** — 网盘里新传的电影，播放器里立刻出现；删了的也自动消失，不用每次手动扫描
>
> ✅ **扫码登录，零配置** — 手机扫一扫就搞定，不用 F12 抓 Cookie，过期了再扫一下就好
>
> ✅ **看片不卡，零带宽消耗** — 直接走 115 原链接播放，不经过你的服务器中转，4K 秒开
>
> ✅ **Emby 深度联动** — 在 Emby 里删了片，网盘对应的本地文件自动清理，不占磁盘
>
> ✅ **异常主动通知** — Cookie 过期、账号异常，Telegram 第一时间推送，不用自己盯着看
>
> ✅ **Go 单二进制** — 纯 Go 架构，部署只需一个文件，内存占用低，启动快

</div>

---

## 🚀 核心特性

| 图标 | 特性 | 说明 |
|:----:|------|------|
| 🆓 | **开源自由** | 基于 115 目录树批量生成 `.strm` 文件，支持自定义前缀配合媒体服务器使用 |
| 👀 | **实时监控** | 115 生活事件轮询，增量同步 + 全量对账双保险 |
| 📱 | **扫码登录** | 手机 App 扫码自动获取 Cookie，无需 F12 抓包；失效后一键扫码刷新 |
| 🔄 | **状态闭环** | 账号异常 TG 自动推送，扫码恢复后自动推送恢复通知 |
| 🔀 | **智能路由** | 302 直连优先 + 静默降级代理；Infuse/VidHub/SenPlayer 自动强制代理，路由参数可在线配置 |
| 🗑️ | **Emby 删除同步** | 监听 `library.deleted` 事件自动清理 STRM，三道防误删保护 |
| 🤖 | **TG Bot 交互** | 支持 Telegram Bot 通知与一键操作（对账 / 清理 / 状态） |
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
| 🐂 **飞牛 fNOS .fpk** | 飞牛 NAS（X86/ARM 机型），一键手动安装 | [GitHub Releases → 选择 `faststrm-{amd64\|arm64}-0.9.5.fpk`](https://github.com/wabisabi926/faststrm/releases) |
| 🖥️ **源码 / 单二进制** | 想自己编译或跑在普通 Linux 主机 | `git clone -b go https://github.com/wabisabi926/faststrm && cd faststrm && go build ./cmd/server/` |

> 📘 飞牛打包、定制、运行目录和排错详见 [docs/飞牛打包部署.md](docs/飞牛打包部署.md)

---

## 🏗️ 架构说明

FastStrm v0.9.1 采用纯 Go 架构：

```
┌─────────────────────────────────────────────────┐
│              FastStrm (Go 单二进制)               │
│  ┌───────────────┐  ┌─────────────────────────┐  │
│  │  go-zero HTTP  │  │  embed.FS (前端 SPA)   │  │
│  │  REST API      │  │  Vite + React 构建产物 │  │
│  └───────────────┘  └─────────────────────────┘  │
│         ↑ 都由 Go 进程直接处理，无需 Nginx         │
└─────────────────────────────────────────────────┘
         ↑
    单端口: 8090
    单二进制: faststrm
```

- **后端**：Go + go-zero，纯 HTTP API
- **前端**：Vite + React SPA，通过 `//go:embed` 嵌入二进制
- **数据库**：SQLite（本地嵌入式）
- **配置存储**：JSON 文件加密存储
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

## 📝 最新版本 (v0.9.5)

- **🔥 跨平台本地目录浏览大修（飞牛 fNOS / Docker / Linux / Windows）**：彻底修复飞牛版本只能看到 `/vol1/@appshare/faststrm` 一个目录、无法选择媒体文件夹的问题
- **📁 后端 4 级优先根列表**：`FASTSTRM_LOCAL_DIR_ROOTS` 显式覆盖 → Linux `/proc/mounts` 真实挂载点 → fNOS 白名单 ∪ NAS 常见卷根（/vol1 /volume1 /mnt/user）→ 硬编码兜底；fNOS 路径白名单默认宽松模式，避免误 403
- **🐂 FNOS manifest 对齐 qmediasync**：新增 `share_dirs` 声明，用户勾选共享文件夹后会真正 bind mount 进应用沙箱
- **🎛️ 前端对话框兜底通道**：本地目录选择器新增手动路径输入框 + Enter/跳转按钮 + 校验状态反馈，根列表不全时可直接粘贴已知路径
- **🏗️ CI：升级 GitHub Actions 到 Node 24 native**（checkout@v5 / setup-node@v5 / setup-go@v6 / upload+download-artifact@v5），消除 Node 20 弃用告警
- **🏷️ 版本号全链路同步**：main.go / package.json / manifest 统一更新为 0.9.5

查看完整变更：[GitHub Releases](https://github.com/wabisabi926/faststrm/releases)
---

## 📄 许可证

本项目采用 [MIT License](LICENSE) 许可证。

## ⚠️ 免责声明

本项目仅供学习和研究使用。请确保你遵守相关的法律法规和服务条款。
