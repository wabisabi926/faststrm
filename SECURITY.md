# 安全策略

## 报告漏洞

如果你发现 FastStrm 的安全漏洞，请**不要**公开提交 Issue。

请通过以下任一渠道私密反馈：

- Telegram 私信项目维护者（群入口见 [Readme.md](Readme.md) 的「交流社群」）
- GitHub 私有安全公告：仓库 → Security → Advisories → New report

收到报告后我们会在 3 个工作日内回复确认，并在修复后公开致谢。

## 支持的版本

只对最新一个 release 分支提供安全更新，详见 [Releases](https://github.com/wabisabi926/faststrm/releases)。

## 已知的安全设计

| 项 | 说明 |
|---|---|
| 用户密码 | AES-GCM 加密存储，不落地明文 |
| 鉴权 | JWT + 中间件校验 |
| 路径遍历 | `filepath.Clean()` 阻断 `..` |
| TLS | 显式强制 `MinVersion: tls.VersionTLS12` |
| 配置文件 | `settings.json` 权限 `0600`，自动迁移新字段并兜底校验 |

## 部署建议

- v1.5.2 起全新安装必须先通过 `/setup` 创建管理员账号；已有安装继续使用原密码，默认 `admin/admin` 仍会被强制要求改密
- 在公网暴露服务前先完成初始化；无界面部署使用 `FASTSTRM_ADMIN_USERNAME` / `FASTSTRM_ADMIN_PASSWORD` 时也请使用强密码
- 在公网暴露服务时务必启用反向代理 + TLS + 强密码
