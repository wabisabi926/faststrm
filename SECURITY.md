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
| STRM 链接 | 可选 HMAC-SHA256 签名 token，防 URL 盗链；默认关闭，向后兼容 |
| TLS | 显式强制 `MinVersion: tls.VersionTLS12` |
| 配置文件 | `settings.json` 权限 `0600`，自动迁移新字段并兜底校验 |
| TokenSecret | 长度异常时自动重新生成，保证 settings.json 损坏后签名仍可用 |

## 部署建议

- 首次登录后立即修改默认 `admin/admin` 账号
- 若 STRM 签名功能开启，确认 `config/settings.json` 所在目录权限受限
- 在公网暴露服务时务必启用反向代理 + TLS + 强密码
