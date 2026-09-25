# MiniTube 测试环境（`minitube-test`）

与生产共用同一集群，隔离在 namespace `minitube-test`。完整对照见仓库 **[docs/environments.md](../../docs/environments.md)**。

| 项 | 值 |
| --- | --- |
| 公网 | `https://minitube-test.19121122.xyz`（Cloudflare 回源 `192.168.43.111:8081`） |
| API | master `hostPort 8081`，镜像 `minitube/api:test` |
| 库 | StatefulSet `minitube-pg`，宿主机 `/data/minitube-pg-test` |
| 媒体 | NFS `/data/minitube-test`（worker2），PVC `minitube-media` |
| 直播 | SRS `hostPort 1936`，`rtmp://192.168.43.111:1936/live` |
| 转码 | worker2 NVENC，连测试库；不申请 `nvidia.com/gpu`，与生产共卡 |
| ctc | 不做。`APP_ENV=test` 强制关闭，管理页灰掉 |

第一次：

```bash
./k8s/minitube-test/bootstrap.sh
```

日常只更测试 API：

```bash
ENV=test ./k8s/minitube/deploy-api.sh
```

不要对测试环境跑 `cutover.sh`（那是生产一次性切流）。
