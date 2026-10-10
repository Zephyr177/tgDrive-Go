# 上游功能对照清单 (Parity Checklist)

上游参考：`SkyDependence/tgDrive`（Java 17 / Spring Boot，clone 位于 `tgDrive/`，只读）。
本清单逐项列出上游功能、Go 实现位置与验证方式；标注“省略/差异”的项说明原因。

| # | 上游功能 | Go 实现 | 验证 |
|---|---|---|---|
| 1 | 登录（用户名或邮箱 + Argon2id），token 下发（sa-token 风格，名称 `tgdrive`，UUID token，有效期 36000000s） | `internal/api/auth.go`、`internal/auth`（Bearer/`tgdrive` 头/Cookie/查询参数均可） | `TestAuthFlow`（集成测试） |
| 2 | Argon2id 密码与上游 Spring 格式兼容（`$argon2id$v=19$m=16384,t=2,p=1$...`），兼容遗留 MD5 | `internal/auth/argon2.go` | `internal/auth/argon2_test.go`（用上游 V7 迁移中的真实哈希验证） |
| 3 | 注册（受 `allow_registration` 开关控制，注册后自动登录；密码≥6、两次一致、用户名/邮箱唯一） | `internal/api/auth.go` | `TestRegisterGated` |
| 4 | 修改密码（用户验证原密码 / 管理员按用户名改密） | `internal/api/auth.go` | `TestAuthFlow`、`TestAdminEndpoints` |
| 5 | 用户管理：列表（id 倒序、密码不返回）、删除（禁止删除 admin） | `internal/api/auth.go` | `TestAdminEndpoints` |
| 6 | 用户角色修改（上游前端调用但后端缺失的 `PUT /api/auth/admin/users/{id}/role`，本实现补齐） | `internal/api/auth.go` | `TestAdminEndpoints` |
| 7 | 文件上传（multipart 字段 `file`；≤10 MiB 直传；>10 MiB 切成 10 MiB 分块 + JSON record 文件；并发 5；SendDocument 失败重试 3 次 1s/2s/4s；小写 `.gif` 去掉后缀） | `internal/files/files.go`、`internal/api/files.go` | `TestFileLifecycle`、`TestChunkedUploadMerge` |
| 8 | 文件下载 `/d/{fileID}`（公开；record 自动合并；Content-Type/Content-Length/Disposition；图片视频 inline 外链；无扩展名时嗅探扩展） | `internal/api/files.go`、`internal/files/files.go` | `TestFileLifecycle`、`TestChunkedUploadMerge`、`TestDownloadHeadersAndSniffing` |
| 9 | 文件列表 `GET /api/file-list`（分页 page/size、keyword、userId；角色过滤：admin 全部 / user 本人+公开 / visitor 仅公开；uploader 联表） | `internal/store/files.go`、`internal/api/files.go` | `TestFileListFiltering` |
| 10 | 文件删除（仅数据库行，不删 Telegram 消息——与上游一致）、公开开关（`PUT /api/file/{fileId}/public`，属主或 admin） | `internal/api/files.go` | `TestFileLifecycle` |
| 11 | 下载 URL 前缀重写 `PUT /api/file-url`（按当前请求前缀重建全部 `download_url`） | `internal/store/files.go` | `TestAdminEndpoints` |
| 12 | 图床/PicGo：无专用端点，使用 `POST /api/upload` + `data.downloadLink`（与上游一致；需携带 token 头） | `internal/api/files.go` | `TestFileLifecycle` |
| 13 | WebDAV `/webdav`：HTTP Basic 认证；停用→503、未认证→401（`WWW-Authenticate: Basic realm="WebDAV"`）、角色不允许→403；PUT/GET/DELETE/OPTIONS/PROPFIND/MKCOL/MOVE/COPY（PROPPATCH 返回 207）；目录行 `file_id='dir'`，上传 `is_public=true`、`user_id=NULL` | `internal/webdav/` | `internal/webdav/webdav_test.go`（TestUnauthorizedWithoutBasicAuth / TestPutThenGetReturnsIdenticalBytes / TestPropfindRootListsUploadedFile / TestMkcolThenPropfindShowsCollection / TestDeleteRemovesFile / TestDisabledConfigReturns503） |
| 14 | WebSocket `/ws/upload-progress`：无鉴权、允许所有来源、广播 `upload_progress` / `upload_complete` / `upload_error`、`ping`→`pong`、5 分钟空闲断开 | `internal/ws/ws.go` | `TestWebSocketProgress` |
| 15 | Bot 配置 CRUD（`/api/config`，upsert 按 name）+ `GET /api/config/{name}` 立即加载生效 + 启动自动加载（优先名称含 default/main） | `internal/api/admin.go`、`internal/bot/bot.go` | `TestAdminEndpoints` |
| 16 | 设置：`GET /api/setting/registration-status`（公开）、列表/更新 `allow_registration`（未知 key 报“该设置不存在”） | `internal/api/admin.go` | `TestRegisterGated`、`TestAdminEndpoints` |
| 17 | 备份：`GET /api/backup/download`（原始 SQLite 文件）；`POST /api/backup/upload`（校验 .db、备份当前库到 `db/tgdrive_backup.db`、按 `file_id` 合并缺失 files 行） | `internal/api/admin.go` | `TestAdminEndpoints`（下载）、`TestBackupUploadMerge`（导入合并） |
| 18 | 保活：每天 02:00 发送保活消息（cron `0 0 2 * * ?`）、start/stop/status/config 接口；下载发送消息 `POST /api/send-message` | `internal/bot/bot.go`、`internal/api/admin.go` | `TestAdminEndpoints`（接口行为）、scheduler 逻辑见 `RunScheduler` |
| 19 | WebDAV 配置：GET/PUT/`status`/`reset`（reset 为仅插入语义，与上游一致） | `internal/api/admin.go`、`internal/store/webdav_config.go` | `TestAdminEndpoints` |
| 20 | 系统监控：上游仅前端伪造统计、无服务端接口；本实现补充真实 `GET /api/system/monitor`（用户数/文件数/运行时长） | `internal/api/admin.go` | `TestAdminEndpoints` |
| 21 | SQLite 表结构与默认数据（users/files/configs/settings/webdav_config + 索引 + 默认 admin/visitor/allow_registration/webdav_config 行） | `internal/store/store.go` 等 | 所有集成测试（每次新建库）+ `TestSeedDefaults` |
| 22 | SPA 前端（上游为 Vue3 构建产物）；本仓库交付自写简化 UI（登录/文件列表/上传下载/管理页，内嵌资源，不移植上游代码） | `internal/ui/` | `TestUIServedAndCORS`、真实浏览器冒烟（登录→文件列表→系统监控） |
| 23 | CORS：`/api/**` 允许所有来源（无凭据）；WebSocket 允许所有来源 | `internal/api/api.go` | `TestUIServedAndCORS`、`TestWebSocketProgress` |
| 24 | 上传统计/进度事件与上游字段一致（percentage/currentChunk/totalChunks 等） | `internal/ws/ws.go` | `TestWebSocketProgress` |

## 有意省略 / 行为差异（均为死代码或明显缺陷，不影响功能对齐）

| 项 | 上游行为 | 本实现 | 原因 |
|---|---|---|---|
| 静态资源 `/uploads/**` 与 `app.upload.path` | 挂载了本地上传目录，但没有任何代码写入 | 未挂载（保留配置项） | 上游死代码 |
| `files.messageID` 列 | 已建列但从未写入；导致 Telegram 消息删除能力失效 | 保留列（兼容 schema），同样不使用 | 行为一致；避免伪功能 |
| `/d/{fileID}` 的 `is_public` 校验 | 不校验（公开可下载） | 同样不校验 | 保持上游行为 |
| 下载合并的 3 线程管道 | 名义 3 并发，实际 1KiB 管道缓冲下近似串行 | 顺序流式合并 | 结果一致；实现更简单可靠 |
| 上传实现内存占用 | 全量载入内存 | 分块缓冲区有界并发（≤5×10MiB） | 同语义、更低内存 |
| SQL 拼接 | keyword/userId 直接拼接（注入风险） | 全部参数化 | 安全修复 |
| 注册重复用户名返回 | 未映射异常（500） | `code 0 + 用户名已存在` | 合理修复 |
| 文件不存在时的 404 | 多返回 500 | 返回 404/`code 0` | 合理修复 |
| 文件日志 `logs/application.log` | logback 落盘 | 输出到标准输出 | 容器化更常规；功能无差异 |
| 上游前端中 3 个未路由的 v2 页面调用的虚构 API（`/api/files/user` 等） | 后端不存在 | 未实现 | 上游死代码 |

## 关键契约（与上游保持一致的线格式）

- 统一响应封装：`{"code":1|0,"msg":string|null,"data":T|null}`；业务失败 HTTP 200 + `code 0`；未登录 HTTP 401；参数校验 HTTP 400。
- 分页：`{"total":number,"records":[...]}`；文件记录字段：`fileName/downloadUrl/uploadTime/size/fullSize/fileId/messageId/webdavPath/dir/userId/uploader`，其中公开标志序列化为 `public`（与上游 Jackson 推导一致）。
- 登录返回：`{token, UserId, role, username, email, expireAt}`（注意 `UserId` 大写，`expireAt` 为毫秒时间戳）。
