# web/

Phase 2 播放页：[`watch.html`](watch.html)，由 API 提供 `GET /watch/{videoId}`。

Phase 3 页面同样由 API 提供：`/`、`/compose`、`/feed`、`/u/{username}`、`/c/{handle}`、`/post/{id}`。源文件在 `api/internal/httpapi/`（go:embed），本目录保留播放页副本。

Phase 5 直播页：`/live`、`/live/{id}`。HLS 经 API 反代，聊天用 SSE。
