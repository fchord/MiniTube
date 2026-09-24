# worker/

FFmpeg 转码进程，与 API 共用数据库和 `UPLOAD_DIR`。

```bash
# 仓库根目录，先 make up 且 API 已能写库
make worker
# 等价：cd api && go run ./cmd/worker
```

认领 `status=processing` 的视频，产出多档 HLS（不超过片源高度、也不超过 `TRANSCODE_MAX_HEIGHT` 的 360/720/1080）、默认音轨、可选内嵌字幕 VTT、封面，然后把状态写成 `ready` 或 `failed`。

本机默认 `TRANSCODE_ENCODER=libx264`。k8s 里按硬编瀑布起多个 worker：

| Rank | 节点 | 编码器 | `TRANSCODE_ENCODER` |
| --- | --- | --- | --- |
| 1 | k8s-worker2 | GTX 1050 Ti NVENC | `h264_nvenc` |
| 2 | k8s-master | GTX 960 NVENC | `h264_nvenc` |
| 3 | k8s-worker2 | UHD 630 QSV | `h264_qsv` |
| 4 | k8s-worker1 | HD 2000 VAAPI | `h264_vaapi` |

只有当前空闲且 rank 最高的 worker 会抢新任务；更高档忙碌或掉线时才落到下一档。硬编设备不可用时任务退回队列，该 worker 标为不健康。
