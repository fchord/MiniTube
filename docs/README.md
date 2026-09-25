# MiniTube 文档

契约优先。改行为先改本目录，再改 [`api/`](../api/) 实现。

| 文档 | 内容 |
| --- | --- |
| [shorts-playback.md](shorts-playback.md) | 短视频滑动队列、1+1+3 硬解窗口、静音/点击约定、硬解测试页 |
| [shorts-slide.md](shorts-slide.md) | 竖滑过渡：\(e(p)\)、试过的曲线、当前五次+六次、未做回弹 |
| [shorts-av-sync.md](shorts-av-sync.md) | 墙钟 AV 同步：单钟、音频基准、TryToMatch / LostMatch |
| [shorts-rebuffer.md](shorts-rebuffer.md) | PCM 不足时攒码流：冻钟、Worklet hold、听路先 demux 音频 |
| [architecture.md](architecture.md) | API 与媒体分层、默认技术选型 |
| [deploy.md](deploy.md) | 运行前提、本机、k8s 切流、日常更新 API、hostPort、管理页 |
| [environments.md](environments.md) | 生产 `minitube` vs 测试 `minitube-test`：域名、端口、库、NFS、发布 `ENV=` |
| [release-checklist.md](release-checklist.md) | 日常发布前/中/后勾选：配置、产物、密钥、healthz、关键路径 |
| [config.md](config.md) | 环境变量清单：API / worker / Secret，对应 `.env.example` |
| [media-edges.md](media-edges.md) | 码流边缘分流：页面走 Cloudflare，HLS 可走 ctc 直连；配置在 `site_settings`；开关在 `/admin` |
| [client-strategy.md](client-strategy.md) | Flutter 全家桶 + 独立 Web + 统一 API |
| [roadmap.md](roadmap.md) | Phase 0→2 为 MVP，短视频/直播后置 |
| [schema.sql](schema.sql) | PostgreSQL 表结构（与 `api/internal/db/schema.sql` 同步） |
| [openapi.yaml](openapi.yaml) | HTTP API（OpenAPI 3.1） |
