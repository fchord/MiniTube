# 运维可见性：健康检查与确认方式

先「能确认活着」，不做完整监控栈。发布时勾 [release-checklist.md](release-checklist.md) 里引用本节的项。

## API：`GET /healthz`

进程在听 HTTP 时返回 **204**、无 body。路径在站点根上（**不是** `/v1/healthz`）。不鉴权。

实现：[`api/internal/httpapi/server.go`](../api/internal/httpapi/server.go) 只 `WriteHeader(204)`，**不查 Postgres、不读 NFS**。集群里 `minitube-api` 的 readinessProbe 打这个路径，超时 3s。

| 环境 | 源站 | 公网（Cloudflare） |
| --- | --- | --- |
| 生产 | `http://192.168.43.111:8080/healthz` | `https://minitube.19121122.xyz/healthz` |
| 测试 | `http://192.168.43.111:8081/healthz` | `https://minitube-test.19121122.xyz/healthz` |
| 本机 | `http://127.0.0.1:8080/healthz` | — |

```bash
curl -sS -m 8 -o /dev/null -w "%{http_code}\n" http://192.168.43.111:8080/healthz
# 期望 204；卡住或非 204 = 本机到 master 不通，或 API 进程 / hostPort 有问题
```

**204 只说明 HTTP 进程在。** 不说明库、转码、直播、NFS 正常。ctc 边缘不要用 `/healthz` 探活（只开放媒体路径），见 [media-edges.md](media-edges.md) 的 edge-probe。

公网与源站对照（主站是 **橙云代理 + 回源端口**，不是 Cloudflare Tunnel）：

- 公网 204、打 `192.168.43.111:8080`（测试 **8081**）超时：回源已通，查你这台机器到 master 的局域网。
- 源站 204、公网失败：查 DNS（橙云）、回源 IP/端口（生产 **8080**、测试 **8081**）、SSL 模式。
- Pod `Pending` 且另有 Running：hostPort 死锁，见 [deploy.md](deploy.md)。

## 服务就绪（API 之外）

```bash
# 生产；测试把 -n minitube 换成 minitube-test
kubectl -n minitube get pods -o wide
```

| 看什么 | 正常时 | 说明 |
| --- | --- | --- |
| `minitube-api` | 1 个 Running，节点 `k8s-master` | 再打上表 `healthz` |
| `minitube-pg-0` | Running | 读库：`curl -sS -m 8 https://minitube.19121122.xyz/v1/public/site` 应是 JSON（`shortsEngine` 等）。500/超时 = 库或站点设置 |
| `minitube-srs` | Running，生产占 **1935**、测试 **1936** | 推流能否连上；HLS 是否出片 |
| `minitube-worker-nvenc-w2` | Running，节点 `k8s-worker2` | 日志里有 `transcode worker registered`；库表 `transcode_workers.last_seen` 约 2s 更新。测试 worker 不申请 `nvidia.com/gpu` |
| NFS | API Pod 能 `ls /data/uploads` | 生产 `/data/minitube`，测试 `/data/minitube-test`。不要为排障去 apply `nfs-prep.yaml`（会重启 nfsd） |

日志：

```bash
kubectl -n minitube logs deploy/minitube-api --tail=80
kubectl -n minitube logs -l app=minitube-worker --tail=80 --prefix
```

开发环境验证邮件 token 打在 API 日志 `verification email (dev)`。

## 和 `/healthz` 的差别

| 检查 | 证明什么 |
| --- | --- |
| `GET /healthz` → 204 | API 进程 + 该端口通 |
| 首页 `200` | 嵌入页能出（不经库） |
| `GET /v1/public/site` | API **能读库** |
| worker `registered` / `last_seen` | 转码进程连着**这个 namespace 的库** |
| 上传后 `videos.status` | 转码任务在跑或已 `ready`/`failed` |
