# api/

Go HTTP API，契约见 [`docs/openapi.yaml`](../docs/openapi.yaml) 与 [`docs/schema.sql`](../docs/schema.sql)。

Phase 1：注册 / 登录 / 邮箱验证 / 资料 / 头像 / 频道 / 订阅。
Phase 2：长视频上传、转码入队、HLS 播放、评论 / 点赞 / 收藏 / 观看记录。播放页：`GET /watch/{videoId}`。
Phase 3：发帖（多图 + 正文）、用户/频道时间线、订阅 Feed、评论与点赞。页面：`/`、`/compose`、`/feed`、`/u/{username}`、`/c/{handle}`、`/post/{id}`。
Phase 4：`kind=short` 走同一转码管线；`GET /v1/feed/shorts`；页面 `/shorts`、`/shorts/{videoId}`（竖屏卡片 + 滚轮/方向键）。
Phase 5：创建直播、OBS/ffmpeg 推 RTMP（`rtmp://主机:1935/live/{密钥}`）、HLS 观看、SSE 聊天；HLS 全量落盘即录播，关播后同一播放地址可回看。页面：`/live`、`/live/{id}`。Origin 为 SRS（`make up`）。

另开终端跑 worker：`make worker`。`UPLOAD_DIR` 必须与 API 相同。

```bash
# 仓库根目录
make up
cd api && go run ./cmd/api
```

环境变量见 [`.env.example`](../.env.example) 与 [docs/config.md](../docs/config.md)。开发环境验证邮件 token 打在日志里；测试使用内存 mailbox。

头像直传走本服务 `PUT /v1/uploads/{token}`（本地磁盘），公开地址 `GET /v1/media/...`。Phase 2 再换成 S3 预签名。
