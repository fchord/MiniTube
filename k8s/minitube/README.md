# MiniTube on this k8s 集群

三节点 homelab：共享盘用 worker2 上的 **内核 NFS**（`/data/minitube`），应用仍走本地目录。转码 worker 按硬编瀑布抢任务。完整步骤、前提、日常更新与 DoD 见仓库 **[docs/deploy.md](../../docs/deploy.md)**。测试环境（另一 namespace）见 **[docs/environments.md](../../docs/environments.md)** 与 [`k8s/minitube-test/`](../minitube-test/)。

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

```bash
chmod +x k8s/minitube/cutover.sh k8s/minitube/build-images.sh
./k8s/minitube/cutover.sh
```

脚本行为见 [docs/deploy.md](../../docs/deploy.md)「第一次切到集群」。Cloudflare 隧道打 `192.168.43.111:8080`。电信码流边缘 `192.168.43.111:18080`，见 [media-edges.md](../../docs/media-edges.md)。推流 `rtmp://192.168.43.111:1935/live`。

## 只更新 API 镜像

不要只 `rollout restart`。先把新镜像 import 进 master 的 containerd，并处理 hostPort 死锁：

```bash
ENV=prod ./k8s/minitube/deploy-api.sh
```

同时更新 worker 才跑 `./k8s/minitube/build-images.sh`，再 `kubectl -n minitube rollout restart deploy -l app=minitube-worker`。
