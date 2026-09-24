# app/

Flutter 客户端，覆盖 Android、iOS、iPadOS、Windows、macOS、Linux。

短视频滑动、上传、桌面播放、直播观看都在本目录。播放协议以 HLS 为主。

Phase 4 API 已就绪：`POST /v1/videos` 带 `kind=short`，发现流 `GET /v1/feed/shorts`。
Phase 5 API 已就绪：`POST /v1/live`、HLS `/v1/live/{id}/playback`、聊天 SSE。Flutter 工程尚未创建；Web 先用 `/shorts` 与 `/live` 验收。
