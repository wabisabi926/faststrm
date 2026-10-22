# `/api/strm`

公开播放端点，播放器无法携带 JWT，因此不要在这里增加需要登录的校验。

## 请求

```http
GET /api/strm?account=alice&pickcode=d18nbyd6w552vx2zs&file_name=电影.iso
HEAD /api/strm?...            # 探测大小/Headers
```

| 参数 | 必填 | 说明 |
|---|---|---|
| `account` | 是 | FastStrm 账号名 |
| `pickcode` | 是 | 17 位字母数字 |
| `file_name` | 否 | 用于 Content-Disposition、容器识别；ISO 必须保留 `.iso` |
| `mode` | 否 | `proxy` / `redirect`，仅私网 IP 生效，调试用 |
| `redirect` | 否 | 内部参数；HEAD + `redirect=1` 时，Redirect 决策返回真实 302 CDN URL |

## 决策

1. 私网 `mode` 覆盖。
2. UA 命中 `forceProxyUaTokens` → 代理。
3. 格式命中 `forceProxyFormats` → 代理。
4. 默认 302；302 前做 CDN HEAD 预检，失败回退代理。

原盘代理不降级 302；普通格式并发超限降级 302。

## 响应

- 302：`Location: <CDN URL>`，带 `Content-Disposition`。
- 代理：透传上游 206/200，`Accept-Ranges: bytes`，严格校验 Range 分片。
- HEAD：优先使用 115 download API 的 `file_size`，不访问 CDN；缺失时用 `Range: bytes=0-0` 探测总大小。
- 错误：`400` 参数/ pickcode，`404` 账号不存在，`502` 取链失败/上游失败。

## 诊断

`GET /api/strm/diagnose?account=&pickcode=&file_name=&ua=`（JWT）复现决策并探测 CDN 头，不返回 CDN URL/Cookie。返回 `decision`、`reason`、`seekFormat`、`rangeProxy`、`proxyInUse`、`proxyLimit`。

## 安全

- 端点公开；URL 泄露即等同于该账号该文件可被取流。
- 不记录完整 CDN 签名 URL、Cookie、Bot Token。
- 生产环境建议放在反代/网络访问控制之后，避免把 8090 直接暴露公网。
