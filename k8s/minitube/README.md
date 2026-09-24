# MiniTube on this k8s 集群

三节点 homelab：共享盘用 worker2 上的 **内核 NFS**（`/data/minitube`），应用仍走本地目录。转码 worker 按硬编瀑布抢任务。

| Rank | Deployment | 节点 | 编码器 |
| --- | --- | --- | --- |
| 1 | `minitube-worker-nvenc-w2` | k8s-worker2 | GTX 1050 Ti `h264_nvenc` |
| 2 | `minitube-worker-nvenc-master` | k8s-master | GTX 960 NVENC，最高 720p |
| 3 | `minitube-worker-qsv-w2` | k8s-worker2 | UHD 630 `h264_qsv`（可与 P1 并行） |
| 4 | `minitube-worker-qsv-w1` | k8s-worker1 | HD 2000 `h264_vaapi`，节点 NotReady 时 Pending |

S3 / SeaweedFS 改写放到二期。本地 `make worker` 仍是 `libx264`。

NVIDIA 用户态与内核模块必须同版本。worker2 开了 `unattended-upgrades`，驱动包升完若未重启，会出现 `Driver/library version mismatch`（NVENC Pod CrashLoop）。修法：重启该节点（它同时是 NFS，会抖一下媒体盘）。不要指望只 reload 模块。

镜像不走 Docker Hub 装 ffmpeg（本机 daemon 代理 `192.168.43.110:10809` 经常挂）：`bundle-ffmpeg.py` 把宿主机 `ffmpeg`/`ffprobe` 和依赖打进 `dist/ffmpeg-bundle`，再 `COPY` 进 `ubuntu:22.04`。

## 一次切到集群

仓库根目录：

```bash
chmod +x k8s/minitube/cutover.sh k8s/minitube/build-images.sh
./k8s/minitube/cutover.sh
```

脚本会：导出 worker2 NFS → 建 PVC → 把 compose 里的 Postgres 迁进 StatefulSet → rsync `api/data` → 编镜像并 ctr import → 停本机 8080/1935 → 拉起 API / SRS / GPU worker。Cloudflare 隧道继续打 `192.168.43.111:8080`（API `hostPort: 8080`）。电信码流边缘 TLS 监听 `192.168.43.111:18080`（`hostPort: 18080`），见 [`docs/media-edges.md`](../../docs/media-edges.md)。直播推流 `rtmp://192.168.43.111:1935/live`。

## 只更新镜像

```bash
./k8s/minitube/build-images.sh
kubectl -n minitube rollout restart deploy/minitube-api
kubectl -n minitube rollout restart deploy -l app=minitube-worker
```
