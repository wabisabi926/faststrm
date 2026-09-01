<div align="center">

  <img src="https://raw.githubusercontent.com/wabisabi926/faststrm/refs/heads/main/frontend/public/logo.png" alt="Fast Strm Logo" width="160" style="margin-bottom: 8px;">

  # Fast Strm

  **FastStrm — 让 115 网盘和你的播放器真正「同步」**

</div>

<div align="left">

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
> ✅ **Docker 一键启动** — 一行命令搞定，5 分钟内看完第一部电影

</div>

<div align="left">

  [![GitHub](https://img.shields.io/badge/GitHub-wabisabi926%2Ffaststrm-181717?logo=github&style=flat-square)](https://github.com/wabisabi926/faststrm)
  [![Version](https://img.shields.io/github/v/release/wabisabi926/faststrm?color=blue&label=Release&logo=semver&style=flat-square)](https://github.com/wabisabi926/faststrm/releases)
  [![License](https://img.shields.io/github/license/wabisabi926/faststrm?color=green&label=License&style=flat-square)](https://github.com/wabisabi926/faststrm/blob/main/LICENSE)
  [![Docker Pulls](https://img.shields.io/docker/pulls/wabisabi926/faststrm?label=Docker%20Pulls&logo=docker&style=flat-square)](https://hub.docker.com/r/wabisabi926/faststrm)
  [![Issues](https://img.shields.io/github/issues/wabisabi926/faststrm?color=orange&label=Issues&style=flat-square)](https://github.com/wabisabi926/faststrm/issues)

</div>

---

## 🚩 目录

- [✨ 项目简介](#-项目简介)
- [🔥 核心亮点](#-核心亮点)
- [📚 文档导航](#-文档导航)
  - [🚀 入门必读](#入门必读)
  - [⚙️ 功能详解](#功能详解)
  - [❓ 排错与参考](#排错与参考)
- [🛠️ 技术架构](#️-技术架构)
- [📦 快速部署](#-快速部署)
- [🤝 相关项目](#-相关项目)
- [📄 开源协议](#-开源协议)

---

## ✨ 项目简介

**Fast Strm** 帮你把 115 网盘里的影视 / 音乐，无缝接入 **Emby / Jellyfin / Kodi** 影音库——

- 播放器里**看起来就是本地文件**：海报、评分、收藏、播放进度全部正常，刮削一次搞定
- **不下载任何东西**：`.strm` 只是个"小指针"，播放时直接从 115 CDN 流式传输，4K 秒开、不占硬盘
- **实时同步，全自动**：30 秒感知网盘增删改，新上传的电影立刻生成 `.strm` 出现在播放器里，删了的自动清理空目录，不用每次手动扫描

特别适合：115 网盘会员 + 家庭 NAS / 小主机 + Emby/Jellyfin 影音库玩家。

---

## 🔥 核心亮点

### 🛰️ 智能路由：该走代理的代理，该直连的直连

很多播放器（比如 Infuse）拖动进度条时需要服务器支持 **Range seek**，直接从 115 CDN 拿片会卡住或跳回开头。Fast Strm 会**自动识别你用什么播放器**，选择最佳播放方式：

- **Infuse / VidHub / SenPlayer** → 自动走代理，进度条随便拖不卡
- **Emby / Kodi / 浏览器** → 默认直连 115 CDN，零服务器带宽消耗
- **115 CDN 临时挂了？** → 自动降级代理，不让你白等
- **账号安全保护** → 自动控制并发数量，不让 115 封你号

> 📖 [详细路由策略说明](STRM-路由策略)

### 🧾 任务历史 + STRM 历史查询

- **任务执行历史**：每次全量扫描 / 手动执行完整记录（开始/结束时间、处理文件数、删除数、错误数），可按任务名筛选
- **STRM 变更历史**：每个 STRM 文件的创建/更新/删除时间戳，支持按账号、路径、文件名搜索
- **实时日志推送（SSE）**：任务执行中实时推送进度日志到前端，不用轮询刷新

### 📡 实时监控：网盘一变，播放器跟着变

10 秒感知 115 网盘里的任何变化，**全自动维护本地 `.strm` 文件**：

- 🆕 网盘**新上传**电影 → 立刻生成 `.strm`，播放器里马上能看到
- 🗑️ 网盘**删了** → 自动清理对应的 `.strm` 和空目录
- ✏️ 网盘**重命名 / 移动** → 自动重建路径，不用手动改
- 🛡️ **防误删保护**：单次删除超过 100 条或占比超 50% 自动熔断，防止 API 异常时误清本地文件

> 📖 [生活事件监控详解](生活事件监控) · [清理对账功能](STRM-清理对账)

### 📺 Emby 深度集成：从网盘到播放器，双向打通

Fast Strm 不只是"生成 STRM 文件"，它会和你的 Emby 影音库**双向联动**，做到真正无缝：

- **删片自动清理**：你在 Emby 里删了一部电影，Fast Strm 会自动删掉对应的 `.strm` 文件——三层保险（文件还在？名字对得上？是不是大量误删？），不会因为误操作清光本地
- **入库更智能**：新 STRM 生成后，等 Emby 刮削完海报评分才发通知，不会让你点开一片空白
- **Emby for Kodi / iOS 播放修复**：这两个客户端播放 ISO 原盘时，Emby 会强转码 HLS 或直接报「没有兼容的流」——开启 Emby 反向代理后，Fast Strm 自动告诉 Emby「这个直接播就行」，ISO、BDMV 统统秒开
- **试运行保护**：首次配置删除同步时默认只记日志不删，确认没问题了再开

> 📖 [Emby 集成全攻略](Emby集成)

### 🔐 防盗链保护（URL 签名）

如果你家 NAS 开了外网访问端口，别人拿到你的 STRM 链接就能直接用你的 115 账号看片——**消耗你的流量、浪费你的并发配额**。开启 URL 签名后：

- 每次播放都会带上一个**动态生成的短时口令**，只有 Fast Strm 自己知道怎么生成
- 链接被人偷走了也没用，过期自动失效
- 默认关闭，按需开启；老用户不用改任何现有配置

### 💬 Telegram 通知：家里有动静，手机立刻知道

绑定 Telegram Bot 后，所有重要事件**自动推送到你手机**：

- 🎬 **任务跑起来了**：开始 / 完成 / 失败 + 实时进度条，不用盯着浏览器刷
- 📺 **Emby 有新片入库** / 删片了：第一时间告诉你
- 🔄 **115 账号 Cookie 过期了 / 恢复了**：主动告警，不用自己去翻日志排查
- 🧩 多集更新自动合并一条消息，不会刷屏轰炸你

> 📖 [TG Bot 配置教程](Telegram-通知)

### 🔒 安全 & 部署

- **扫码登录**：手机 App 扫码获取 Cookie，7 种客户端类型，过期可一键刷新
- 115 Cookie / Emby API Key / TG Token：**AES-256-GCM** 加密 · 密码：**SHA-256 + 唯一 salt** · 会话：**JWT**
- **单二进制 SPA 部署**：Go embed 内嵌前端产物，一个文件搞定
- **Docker** 多架构（linux/amd64 + arm64）· fNOS 原生包

---

## 📚 文档导航

### 🚀 入门必读

| 序号 | 文档 | 适合人群 | 你将学会 |
|:----:|------|---------|---------|
| 1 | **[🏁 快速开始](快速开始)** | 新用户 | 首次使用全流程：登录 → 加账号 → 建任务 → 生成 STRM → 接入 Emby |
| 2 | **[📦 安装部署](安装部署)** | 运维 | Docker / 手动 / 生产环境部署，端口说明、目录挂载、升级方法 |
| 3 | **[⚙️ 配置说明](配置说明)** | 高级用户 | settings.json 全字段：UA、扩展名、Emby、TG、下载限流等 |

### ⚙️ 功能详解

| 模块 | 文档 | 关键词 |
|------|------|--------|
| 核心生成 | **[STRM 生成](STRM生成)** | 任务配置、路径映射、302 模式、下载扩展名 |
| 路由播放 | **[STRM 路由策略](STRM-路由策略)** | proxy vs redirect、规则引擎、并发限流、降级逻辑 |
| 实时同步 | **[生活事件监控](生活事件监控)** | 增量同步、4 类事件、删除熔断、API 降级 |
| 数据质量 | **[STRM 清理对账](STRM-清理对账)** | 孤儿扫描、全量对账、监控暂停、防误删 |
| Emby 双向 | **[Emby 集成](Emby集成)** | 媒体库刷新、Webhook 通知、删除同步、路径映射 |
| 消息推送 | **[Telegram 通知](Telegram-通知)** | Bot 配置、通知类型、轮询模式、交互命令 |

### ❓ 排错与参考

- **[💡 FAQ](FAQ)**：常见问题、排错思路、已知限制、最佳实践

---

## 🏗️ 系统架构

```
┌──────────┐     ┌──────────────────┐     ┌──────────────┐
│ 115 网盘  │────▶│  FastStrm 处理引擎  │────▶│  本地 STRM   │
│ 变动事件  │     │ 轮询→分类→映射→处理  │     │  + 媒体服务器  │
└──────────┘     └──────────────────┘     └──────────────┘
```

**核心流程**：115 网盘产生变动事件 → 每 30 秒轮询拉取 → 事件分类 + 路径映射 → 生成/删除 STRM → 触发 Emby 刷新

---

## 🛠️ 技术架构

| 层级 | 技术选型 |
|------|---------|
| 后端 | **Go 1.25** · go-zero REST 框架 · 原生并发（goroutine + semaphore） |
| 前端 SPA | **Vite + React + TypeScript** + shadcn/ui + Tailwind CSS + Radix UI |
| 部署形态 | **单二进制**：Go embed 内嵌前端产物，`go build` 一个文件搞定 |
| 数据库 | **SQLite** (mattn/go-sqlite3，file_path / sync_del 持久化 + schema migrations) |
| 115 协议 | p115client 理念自研 SDK（Web + iOS API 双通道） |
| 速率控制 | 账号级令牌桶 + 并发信号量 + 指数退避重试 |
| 部署 | **Docker**（linux/amd64 + linux/arm64，多架构构建）· fNOS 原生包 |
| STRM 代理 | **纯 Go 反代**（原生 302/Range 兼容 + HMAC-SHA256 URL 签名，无额外 Nginx） |

```
  ┌─────────────┐   ┌─────────────┐   ┌──────────────┐
  │   客户端     │   │  Emby Server│   │  115 网盘 API │
  │ Infuse/Kodi │──▶│   (可选)    │──▶│    CDN        │
  └─────────────┘   └──────┬──────┘   └──────┬───────┘
                           │                  │
                           ▼                  ▼
                  ┌─────────────────────────────┐
                  │     Fast Strm (:8090)        │
                  │  ┌─────────┐  ┌───────────┐  │
                  │  │ 路由引擎 │  │  STRM 生成 │  │
                  │  └─────────┘  └───────────┘  │
                  │  ┌─────────┐  ┌───────────┐  │
                  │  │ 生活监控 │  │ TG 通知   │  │
                  │  └─────────┘  └───────────┘  │
                  │  ┌─────────────────────────┐  │
                  │  │ SQLite / filePathDb     │  │
                  │  └─────────────────────────┘  │
                  └─────────────────────────────┘
```

---

## 📦 快速部署

### Docker Compose（推荐）

```bash
git clone -b go https://github.com/wabisabi926/faststrm.git
cd faststrm
docker-compose up -d
```

启动后访问：`http://<你的IP>:8090`，默认账号密码：**admin / admin**（首次登录请立即修改）。

> 📖 完整部署流程（目录挂载、端口说明、升级）：[安装部署](安装部署)

---

## 🤝 相关项目

感谢以下开源项目提供参考与灵感：

- [p115client](https://github.com/ChenyangGao/p115client) — 115 网盘 Python 客户端（协议参考）
- [embyExternalUrl](https://github.com/bpking1/embyExternalUrl) — Emby 302 反代思路
- [qmediasync](https://github.com/qicfan/qmediasync) — 同类 STRM 生成器（Emby 通知 / fNOS 打包参考）
- [Alist](https://github.com/alist-org/alist) — 多网盘挂载方案
- [Openlist](https://github.com/OpenListTeam/OpenList) — 115 目录列表工具
- [MoviePilot-Plugins](https://github.com/DDSRem/MoviePilot-Plugins) — Emby 删除同步（samediasyncdel）移植参考
- [openStrm](https://github.com/indown/openStrm) — 本项目前身

---

## 📄 开源协议

本项目采用 **[MIT License](https://github.com/wabisabi926/faststrm/blob/main/LICENSE)** 协议开源，欢迎 Fork、二次改造与商业使用。

---

<div align="center">

  **Enjoy your media library, powered by 115 & Fast Strm.** 🎬

  [⬆️ 返回顶部](#fast-strm)

</div>
