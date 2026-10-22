# Windows 运行

## 启动

```powershell
.\faststrm.exe --no-tray -c D:\faststrm-config
```

- 不加 `--no-tray` 时使用系统托盘。
- `-c/--config` 指定配置目录；不指定时按 `DEFAULT_CONFIG_DIR`/`CONFIG_DIR`/可执行文件旁 `.config` 探测。
- 默认监听 `0.0.0.0:8090`，浏览器访问 `http://localhost:8090`。

## 反代

- 反代默认 `0.0.0.0:8097`；`0.0.0.0` 是监听地址，客户端用 `http://<本机 IP>:8097`。
- 如果 8097 被占用，反代会启动失败；在「设置 → 通知中心 → Emby」修改端口后保存。
- Emby 本体仍可用 8096；但播放原盘必须让客户端连 8097。

## 测试包

测试包不要使用线上配置目录；用临时目录启动，避免污染真实账号/数据库。
