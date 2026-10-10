# tgDrive (Go)

基于 Telegram Bot 的网盘应用 —— 用 Go 从零重写的版本。上游参考项目：[SkyDependence/tgDrive](https://github.com/SkyDependence/tgDrive)（Java 17 / Spring Boot）。

## 功能

- 用户注册 / 登录 / 修改密码（Argon2id，与上游格式兼容），管理员用户管理
- 文件上传 / 下载 / 列表 / 搜索 / 删除 / 公开开关
  - 大于 10 MiB 的文件自动分块上传，下载时自动合并（突破 Bot API 20MB 限制）
  - 小文件 GIF 后缀处理（避免 Telegram 转 MP4）
- 图片/视频外链（`Content-Disposition: inline`，`/d/{fileID}` 可直接引用）
- WebDAV 服务（`/webdav`，HTTP Basic，角色白名单）
- WebSocket 上传进度推送（`/ws/upload-progress`）
- 管理后台接口：Bot Token 配置、注册开关、数据库备份、保活任务、WebDAV 配置、系统监控
- 自写简化 Web UI（内嵌静态资源，开箱即用）
- SQLite 持久化（默认 `db/tgDrive.db`）

## 快速开始

```bash
go run ./cmd/tgdrive
# 打开 http://127.0.0.1:8085
```

默认账号：

| 用户名 | 密码 | 角色 |
|---|---|---|
| admin | 123456 | 管理员 |
| visitor | hello | 访客 |

使用前需在管理界面（或 `POST /api/config`）配置 Telegram Bot：`name` / `token` / `target`（chat id），随后 `GET /api/config/{name}` 使其生效（启动时会自动加载名称含 `default` 或 `main` 的配置）。

## 配置（环境变量）

| 变量 | 默认 | 说明 |
|---|---|---|
| `TGDRIVE_PORT` | `8085` | HTTP 端口 |
| `TGDRIVE_DB` | `db/tgDrive.db` | SQLite 路径 |
| `TGDRIVE_UPLOAD_DIR` | `uploads` | 本地上传目录（保留） |
| `TGDRIVE_BOT_API_URL` | 官方端点 | 自定义 Bot API 端点（本地 Bot API 服务器） |

## 开发

```bash
go build ./...    # 构建
go vet ./...      # 静态检查
go test ./...     # 全部测试（含集成测试，使用假 Telegram 后端，无需网络）
```

## 与上游的对应关系

逐项功能对照见 [docs/PARITY.md](docs/PARITY.md)。

## License

MIT。上游项目版权归 SkyDev 所有（详见 [LICENSE](LICENSE)）。
