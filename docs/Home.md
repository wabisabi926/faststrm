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

## 📌 版本公告

> **🎉 v0.8.7 已发布 — 全栈 Go 架构迁移**
>
> - 🏗️ **Go 重构**：彻底移除 Next.js，改为 Go + go-zero 单二进制架构，前端 Vite+React 通过 `go:embed` 嵌入
> - 🔄 **端口统一**：从 3000 端口变更为 8090，单端口托管前端 SPA 和 API
> - 🐛 **Bug 修复**：扫码登录、账户重命名、任务列表等多项修复
> - 📱 **移动端适配**：所有页面响应式优化
> - 🌐 **中文化**：界面文案统一中文
> - 完整变更说明：[GitHub Releases](https://github.com/wabisabi926/faststrm/releases)

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

**Fast Strm** 帮你把 115 网盘里的影视 / 音乐媒体文件生成本地 `.strm` 文件，配合 **Emby / Jellyfin / Kodi** 实现「**云盘资源本地化播放**」——

- 媒体库**看起来像本地文件**，刮削、分类、收藏、播放记录全部正常
- **实际播放时直连 115 CDN**，不占用部署设备硬盘
- 高级路由策略让 **Infuse 稳定拖动进度条**，同时 **Kodi / Emby Web 零中转流量**

特别适合：115 网盘会员 + 家庭 NAS / 小主机 + Emby/Jellyfin 影音库玩家。

---

## 🔥 核心亮点

<table>
<tr>
<td width="50%" valign="top">

### 🛰️ STRM 智能路由

不是「强制 302」或「强制代理」二选一，而是**规则引擎自动决策 + 预检降级**：

- **Infuse / VidHub / SenPlayer** → seek 兼容性差，**强制代理**
- **Emby / Kodi / 浏览器** → 默认 **302 直连** 115 CDN，部署设备零中转
- 302 前 **HEAD 预检**，115 CDN 临时不可达时**静默降级代理**，用户无感
- **115 单账号并发代理限流（阈值 8）**，避免触发 115 约 10 进程上限
- 调用 115 API 解析**真实 `file_size`**，替代不可靠的文件名估算

[📖 阅读完整路由策略](功能详解-STRM路由策略)

</td>
<td width="50%" valign="top">

### 📡 生活事件实时监控

30 秒轮询 115 生活事件，4 类事件自动同步 STRM：

| 事件类型 | 处理动作 |
|---------|---------|
| 📁 创建 / 上传 | 新增 STRM 文件 |
| 🗑️ 删除 | 删除对应 STRM + 清理空目录 |
| 🔄 移动 / 重命名 | 递归搜索旧路径并重建 |

- 3 层路径解析缓存，性能稳定无爆 API
- **删除熔断**：单次 >100 条或占比 >50% 自动跳过，防误删
- iOS API 405 时**自动降级 Web API**

[📖 生活监控使用说明](功能详解-生活事件监控)

</td>
</tr>
<tr>
<td width="50%" valign="top">

### 🧹 STRM 清理与全量对账

- **孤儿文件扫描**：STRM 指向的 pickcode 在 115 不存在时，自动物理删除
- **全量对账**：账号全盘树 vs 本地 STRM 双向比对，查漏补缺
- **监控自动暂停 / 恢复**：扫描前挂起生活监控，避免 API 与 DB 竞争
- **空目录安全清理**：保护映射根目录，防止误删顶层

[📖 清理与对账操作指南](功能详解-STRM清理对账)

</td>
<td width="50%" valign="top">

### 🗑️ Emby 删除同步

监听 Emby `library.deleted` Webhook，自动清理本地侧资源：

> ✅ **三道防误删保护**
> 1. STRM 文件**物理存在检查**，不存在直接跳过
> 2. Movie / Episode **标题匹配**校验，不匹配拒绝删除
> 3. 整季 / 整剧目录**文件数 ≤ 100** 才执行

- 60 秒去重窗口，避免与生活监控重复处理
- **试运行模式**：首次配置只记日志不实际删除
- STRM + 字幕 / nfo / 图片 + 空目录 + DB 记录，**直接物理删除**（不经过回收站，误删可在 115 网盘恢复源文件后重新全量扫描生成）

[📖 Emby 集成全攻略](功能详解-Emby集成)

</td>
</tr>
<tr>
<td width="50%" valign="top">

### 📱 Telegram 通知

随时掌握任务与播放状态：

- 🎬 任务：开始 / 完成 / 失败 + 进度条
- 📺 Emby：入库 / 删除 / 播放 / 停止
- 🔄 **账户状态闭环**：115 账号异常 / 恢复自动推送，扫码刷新后自动发恢复通知
- 🧩 剧集缓冲合并（10 秒窗口），避免刷屏
- 支持轮询模式，无需公网 Webhook

[📖 TG Bot 配置教程](功能详解-Telegram通知)

</td>
<td width="50%" valign="top">

### 🔒 凭据安全 & 扫码登录

- **📱 115 扫码登录**（v0.8.2）：手机 App 扫码自动获取 Cookie，支持 7 种客户端类型，无需 F12 抓包；Cookie 失效可一键扫码刷新
- 115 cookie / Emby API Key / TG Bot Token：**AES-256-GCM** 加密存储
- 登录密码：**SHA-256** 哈希 + 唯一 salt
- 会话管理：**JWT** Token，支持主动登出
- 关键写操作前强制鉴权校验

</td>
</tr>
</table>

---

## 📚 文档导航

### 🚀 入门必读

| 序号 | 文档 | 适合人群 | 你将学会 |
|:----:|------|---------|---------|
| 1 | **[🏁 快速开始](快速开始)** | 新用户 | 首次使用全流程：登录 → 加账号 → 建任务 → 生成 STRM → 接入 Emby |
| 2 | **[📦 安装部署](安装部署)** | 运维 | Docker / 手动 / 生产环境部署，端口说明、目录挂载、升级方法 |
| 3 | **[⚙️ 配置项参考](配置项参考)** | 高级用户 | settings.json 全字段：UA、扩展名、Emby、TG、下载限流等 |

### ⚙️ 功能详解

| 模块 | 文档 | 关键词 |
|------|------|--------|
| 核心生成 | **[STRM 生成](功能详解-STRM生成)** | 任务配置、路径映射、302 模式、下载扩展名 |
| 路由播放 | **[STRM 路由策略](功能详解-STRM路由策略)** | proxy vs redirect、规则引擎、并发限流、降级逻辑 |
| 实时同步 | **[生活事件监控](功能详解-生活事件监控)** | 增量同步、4 类事件、删除熔断、API 降级 |
| 数据质量 | **[STRM 清理对账](功能详解-STRM清理对账)** | 孤儿扫描、全量对账、监控暂停、防误删 |
| Emby 双向 | **[Emby 集成](功能详解-Emby集成)** | 媒体库刷新、Webhook 通知、删除同步、路径映射 |
| 消息推送 | **[Telegram 通知](功能详解-Telegram通知)** | Bot 配置、通知类型、轮询模式、交互命令 |

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
| 前端 / 后端一体化 | **Next.js 15** (App Router + TypeScript + Turbopack) |
| UI 组件 | **shadcn/ui** + **Tailwind CSS** + **Radix UI** |
| 数据库 | **SQLite** (better-sqlite3，file_path / sync_del 持久化) |
| 115 协议 | **p115client** 理念自研 SDK（Web + iOS API 双通道） |
| 任务调度 | 原生 async 调度器 + cron 表达式 |
| 速率控制 | 账号级令牌桶 + 指数退避重试 |
| 部署 | **Docker**（linux/amd64 + linux/arm64，多架构构建） |
| 反向代理 | 内置 **Nginx**（emby2Alist 模块，兼容 302 场景） |

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
