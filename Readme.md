<div align="center">
  <img src="https://raw.githubusercontent.com/wabisabi926/faststrm/refs/heads/main/frontend/public/logo.png" alt="Fast Strm Logo" width="200" height="200">
</div>

# Fast Strm

**🎉 更新通知（v0.5.0）**：filePathDb 迁移至 SQLite（better-sqlite3），新增统一文件操作工具层、mediaMountPath 全量同步、令牌桶限流，生活事件监控与 STRM 清理逻辑全面重构。

> 推荐：如果使用 115 302 的话，建议将 115 账号命名和 OpenList 或 CD 内命名一致，这样可以保证找不到地址的时候可以正确回源。
>
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

## 📄 许可证

本项目采用 [MIT License](LICENSE) 许可证。

## 🤝 贡献

欢迎提交 Issue 和 Pull Request 来改进这个项目。

## ⚠️ 免责声明

本项目仅供学习和研究使用。请确保你遵守相关的法律法规和服务条款。
