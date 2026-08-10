<div align="center">
  <img src="https://raw.githubusercontent.com/wabisabi926/faststrm/refs/heads/main/frontend/public/logo.png" alt="Fast Strm Logo" width="200" height="200">
</div>

# Fast Strm

**🎉 更新通知（v0.6.0）**：新增 Emby Webhook 通知与 Emby 通知设置页面，Telegram 轮询控制全面中文汉化，通知 API 路由重构统一至 `/api/notify/*`，优化设置页面头部对齐、STRM 清理卡片布局、Sidebar 切换按钮居中对齐，版本号正式升级为 v0.6.0。

**📌 上一版本（v0.5.0）**：filePathDb 迁移至 SQLite（better-sqlite3），新增统一文件操作工具层、mediaMountPath 全量同步、令牌桶限流，生活事件监控与 STRM 清理逻辑全面重构。

> 推荐：如果使用 115 302 的话，建议将 115 账号命名和 OpenList 或 CD 内命名一致，这样可以保证找不到地址的时候可以正确回源。

---

## 📝 版本更新日志

### v0.6.0

- **Emby 通知接入**
  - 新增 `/api/emby/webhook` 接口，可接收 Emby 播放/停止/测试等事件并转发至 Telegram
  - 新增「Emby 通知」页面，引导用户配置 Webhook URL 及事件选择（播放、停止、播放进度、用户操作等）
  - 新增 `src/lib/emby/` 模块：`client`（系统/会话查询）、`notifier`（播放状态/测试事件格式化）、`types`（事件类型）

- **TG 通知页面全中文**
  - 轮询状态「Polling is active / not active」→「轮询中 / 轮询未启动」
  - 启动轮询、停止轮询、刷新状态等 API 响应消息全部汉化
  - 强制清理失败等错误消息改为中文提示

- **路由与页面重构**
  - 原 `/api/telegram/*` 全部重命名迁移至 `/api/notify/*`（`bot` / `polling` / `send` / `users` / `webhook`）
  - 原 `/telegram` 页面迁移至 `/notify` 主页面 + `/notify/users` 用户管理页
  - 新增 `/tg-notify` 独立的 TG 通知配置页

- **UI / 布局优化**
  - 顶部导航栏：将 SidebarTrigger 移入 header 内部，与分享链接输入框、GitHub/设置图标在同一行自动垂直居中对齐
  - STRM 清理卡片：标题与描述间距加大，描述文字下移，两个操作按钮（扫描路径映射、全量对账）与左侧文字块垂直居中
  - 清理卡片状态提示、按钮加载文案统一为中文
  - 分享详情对话框、任务对话框等细节中文微调

- **安全性与忽略配置**
  - `.gitignore` 新增 `.config/` 目录（原项目配置存储路径），避免本地账号/任务 JSON 被误提交
  - 保留 `config/`、`logs/`、`data/` 等运行时目录忽略规则

- **版本一致性**
  - 左下角版本号由 `package.json` 统一驱动，升级为 `v0.6.0`
  - GitHub Release workflow 版本写入从 `npm version` 改为直接 JSON 写回，避免「Version not changed」导致构建失败

- **STRM 路由策略（方案 B）**
  - `/api/strm?account=...&pickcode=...` 不再是「强制代理」或「强制 302」二选一，而是**规则引擎自动决策 + 预检降级**
  - 规则优先级：① 手动 `?mode=` > ② Infuse/VidHub/SenPlayer 等 seek 坑客户端（强制代理） > ③ 局域网访问（强制代理，稳定优先） > ④ 文件名命中 ≥20GB 大文件标识（`*.20GB.* / *.45.3G.* / *.12000MB.*`，优先 302 省服务器上行） > ⑤ 其他默认代理（开箱即用不出错）
  - 302 之前后端先做 `redirectCheck`：后端自己 HEAD 一次 115 CDN 直链（带正确 UA/Cookie/Referer/Origin，5s 超时），返回 2xx/3xx 才真正 302 给客户端；否则**静默降级为代理**（用户无感，不会看到「无法访问此页面」）
  - 两层 LRU 缓存：URL 解析 512 条 / 5min，HEAD 可达性 256 条 / 4min，避免重复请求 115 接口
  - 统一日志格式 `[STRM] pickcode=xxxx…xxx decision=proxy|redirect reason=<规则命中原因> redirect_check=200|403|skipped elapsed=xxxms`，方便排障
  - 调试参数：追加 `?mode=proxy` 或 `?mode=redirect` 可手动强制模式，绕过规则引擎

### v0.5.0

- filePathDb 迁移至 SQLite（better-sqlite3）
- 新增统一文件操作工具层
- 新增 mediaMountPath 全量同步
- 新增账号级令牌桶限流与重试逻辑
- 生活事件监控与 STRM 清理逻辑全面重构

> **前置配置**：
> 1. 请在项目内配置好 Emby 的地址以及 API Key
> 2. 新建同步任务时开启 302 开关

---

一个开源的 **Strm 生成工具**。基于 open strm 项目魔改，加入 115 生活事件轮询。感谢原作者。

## ✨ 为什么做这个软件

希望此项目能帮助大家更简单创建的自己strm库。  

该项目的目标是：**开放、简洁、可改造**。  

本项目参考或依赖以下项目： 
- [p115client](https://github.com/ChenyangGao/p115client/)
- [Alist](https://github.com/alist-org/alist)  
- [Openlist](https://github.com/OpenListTeam/OpenList)  
- [embyExternalUrl](https://github.com/bpking1/embyExternalUrl)  
- [rclone](https://github.com/rclone/rclone)  
- [MoviePilot-Plugins](https://github.com/DDSRem-Dev/MoviePilot-Plugins)  
- [openStrm](https://github.com/indown/openStrm)  

## 🚀 特性

- 开源自由
- 支持批量生成 `.strm` 文件
- 支持自定义前缀（方便配合媒体服务器使用）
- 基于115目录树生成
- 支持 115 生活事件实时监控（增量同步 + 全量同步）
- filePathDb 基于 SQLite 持久化，file_id 字符串绑定避免精度丢失
- 支持账号级令牌桶限流和重试逻辑
- 支持定时任务调度
- 支持 STRM 清理与对账（扫描孤儿文件、自动恢复监控）
- 支持 Telegram Bot 通知与交互
- 登录密码 SHA-256 哈希存储，115 cookie / Emby API Key 等凭据 AES-256-GCM 加密
- 轻量，易于二次开发

## 📦 安装 & 使用

### 使用 Docker (推荐)

```bash
# 使用 Docker Compose
git clone https://github.com/wabisabi926/faststrm.git
cd faststrm
docker-compose up -d
```

### 手动构建

```bash
# 克隆项目
git clone https://github.com/wabisabi926/faststrm.git
cd faststrm

# 安装依赖
cd frontend
npm install

# 启动服务
npm run dev
```

### Docker 镜像

项目支持多架构构建 (linux/amd64, linux/arm64)：

```bash
# 拉取最新镜像
docker pull wabisabi926/faststrm:latest

# 运行容器
docker run -d \
  --name faststrm \
  -p 3000:3000 \
  -p 8091:8091 \
  -v $(pwd)/data:/app/data \
  -v $(pwd)/config:/app/config \
  -v $(pwd)/emby2Alist/nginx/log:/var/log/nginx \
  wabisabi926/faststrm:latest
```

**端口说明**：
- `3000`: 前端管理界面
- `8091`: Emby 302 代理端口（Emby 客户端使用此端口连接）

**目录挂载说明**：
- `./data`: 存储应用数据
- `./config`: 存储配置文件
- `./emby2Alist/nginx/log`: Nginx 日志目录

### 生产环境部署

```bash
# 使用生产环境配置
docker-compose -f docker-compose.prod.yml up -d
```

## 🔧 配置说明

容器启动后，配置文件位于 `./config/` 目录下，首次启动会从内置默认模板自动生成。

### 配置文件结构

| 文件 | 说明 |
|------|------|
| `config.json` | 登录凭据（`username` / `password`） |
| `settings.json` | 应用配置（Emby、Telegram、扩展名、下载限流等） |
| `account.json` | 115 / OpenList 账号信息（cookie 等敏感字段已加密） |
| `tasks.json` | 同步任务定义 |

### 默认登录信息

首次启动后，使用以下默认账号登录：

```json
{
    "username": "admin",
    "password": "admin"
}
```

⚠️ **安全提示**: 请在生产环境中及时修改默认密码！  
📝 **修改方法**: 登录后在管理界面修改密码，或编辑 `config/config.json` 文件中的 `username` 和 `password` 字段（密码支持明文与 SHA-256 哈希两种格式，修改后重启生效）。

### 应用配置项（settings.json）

以下配置均可在管理界面「设置」页面修改，也可直接编辑 `config/settings.json`：

- `user-agent`: 用于 115 API 请求的 User-Agent 字符串
- `strmExtensions`: 需要转换为 `.strm` 文件的扩展名数组，默认为 `[".mp4", ".mkv", ".avi", ".iso", ".mov", ".rmvb", ".webm", ".flv", ".m3u8", ".mp3", ".flac", ".ogg", ".m4a", ".wav", ".opus", ".wma"]`，会自动转换为小写
- `downloadExtensions`: 需要直接下载的文件扩展名数组，默认为 `[".srt", ".ass", ".sub", ".nfo", ".jpg", ".png"]`，会自动转换为小写
- `strmPrefix`: STRM 前缀（如 `http://localhost:3000`），不含账号名
- `enable302`: 是否在 strmPrefix 后自动拼接账号名（用于 Emby 302 重定向）
- `enablePathEncoding`: 是否启用 URL 路径编码
- `removeExtraFiles`: 是否自动删除远程已不存在的本地 STRM 文件
- `emby.url`: Emby 媒体服务器地址
- `emby.apiKey`: Emby API 密钥
- `telegram.botToken`: Telegram Bot Token
- `telegram.chatId`: Telegram 通知 Chat ID
- `telegram.allowedUsers`: 允许交互的 Telegram 用户 ID 列表
- `download.linkMaxPerSecond`: 每秒最大链接数
- `download.linkMaxConcurrent`: 最大并发链接数
- `download.downloadMaxConcurrent`: 最大并发下载数

> `mediaMountPath` 由系统根据账号与任务配置自动同步（SSOT），不建议手动修改。

## ⚡ STRM 路由策略（方案 B，默认启用）

**核心思路**：不在「纯 302」和「纯代理」二选一，而是按**客户端 / 网络 / 文件大小**自动选择，302 走不通就**静默降级代理**，保证「浏览器直接打开 STRM 不报错 & 大文件不吃服务器上行」。

接口路径：`/api/strm?account=<账号名>&pickcode=<17位pickcode>&file_name=<可选文件名>`

### 规则优先级（从高到低，命中即停）

| # | 条件 | 决策 | 说明 |
|---|------|------|------|
| ① | 手动指定 `?mode=proxy` 或 `?mode=redirect` | 按参数执行 | 调试用，优先级最高 |
| ② | UA 命中 `Infuse / VidHub / SenPlayer` 等 seek 坑客户端 | **强制代理** | 这些客户端 302 + Range 配合会出现拖动进度条失败 |
| ③ | 客户端 IP 属于局域网（`192.168.*` / `10.*` / `172.16-31.*` / 回环） | **强制代理** | 家里上行够用，稳定性优先，不冒险走 115 防盗链 |
| ④ | `file_name` 命中 ≥20GB 大小标记（如 `.20GB.` `.45.3G.` `.12000MB.`） | **redirect + 预检** | 大文件优先省服务器上行，预检失败再回退 |
| ⑤ | 其他所有情况 | **默认代理** | 浏览器直接打开 / 未知客户端，保证开箱即用 |

### 302 预检（redirectCheck）

当决策命中 redirect 时，后端不会直接把 115 cdnfhnfile URL 甩给客户端，而是先自己做一次**本地 HEAD 校验**：

1. 以**申请该 URL 时相同的 UA + Cookie + Referer=https://115.com/ + Origin=https://115.com** 发起 `HEAD <cdnUrl>`
2. 超时 5 秒；HTTP 2xx/3xx（在 `redirect: follow` 下会落到最终 200）视为**可达**
3. 可达 → 才真正返回 `302 Location: <cdnUrl>` 给客户端
4. 不可达（403 / 超时 / 网络错误）→ **静默降级到代理模式**，追加日志 reason `... -> redirect_check_failed(<status>) fallback_proxy`

> 这一步解决了「浏览器直接打开 115 CDN 链接被防盗链拦 → 无法访问此页面」的根因：当 115 临时改签名或 token 过期时，用户看到的依然是成功播放，只是流量从服务器中转一次。

### 缓存策略（内存 LRU，无外部依赖）

| 缓存 | 容量 | TTL | 用途 |
|------|------|-----|------|
| URL 解析 | 512 条 | 5 分钟 | 缓存 `getDownloadUrlWeb()`（115 android/ufile/download）结果，避免重复申请 |
| HEAD 可达性 | 256 条 | 4 分钟 | 缓存 `redirectCheck` 成功结果，失败不缓存，下次立即重试 |
| LRU 规则 | get 命中重排 | 超容量删最老条目 | 防止长时间运行内存膨胀 |

### 排障日志格式

```
[STRM] pickcode=cscm…mhv decision=proxy  reason=private_network_prefers_proxy           redirect_check=skipped elapsed=112ms
[STRM] pickcode=abcd…xyz decision=redirect reason=large_file_ge_20GB(25GB)               redirect_check=200     elapsed=214ms
[STRM] pickcode=wxyz…999 decision=proxy  reason=large_file_ge_20GB(22GB) -> redirect_check_failed(403) fallback_proxy  redirect_check=403 elapsed=780ms
[STRM] pickcode=xxxx…xxx decision=proxy  reason=force_proxy_ua:Infuse                    redirect_check=skipped elapsed=97ms
```

对应代码见：[`frontend/src/app/api/strm/route.ts`](file:///d:/Downloads/ai/faststrm/frontend/src/app/api/strm/route.ts)

## 📄 许可证

本项目采用 [MIT License](LICENSE) 许可证。

## 🤝 贡献

欢迎提交 Issue 和 Pull Request 来改进这个项目。

## ⚠️ 免责声明

本项目仅供学习和研究使用。请确保你遵守相关的法律法规和服务条款。
