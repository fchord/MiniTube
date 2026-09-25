# 发布检查清单

按一次真实 homelab 发布来勾，不要凭记忆。步骤细节以 [deploy.md](deploy.md) 为准；变量边界见 [config.md](config.md)。文档与脚本冲突时改其中一侧，不要留两套。

对象默认是 **日常只更生产 API**（`ENV=prod`，`namespace minitube`，镜像 `minitube/api:local`）。测试站用 `ENV=test`（`minitube-test`、**8081**），对照 [environments.md](environments.md)。第一次切流用 `cutover.sh`，不走本清单的「发布中」。同时更 worker 才跑 `build-images.sh`。

## 发布前

### 配置与范围

- [ ] 明确这次打的是 **生产** `minitube`（master **8080 / 18080**），不是本机 `make run`，也不是测试站 **8081**
- [ ] 改动已在 PR 里，或至少知道要部署的 git 提交
- [ ] shorts / 嵌入页若有改：`staticAssetVersion` 已 bump
- [ ] 若改了 `k8s/minitube/configmap.yaml`：`deploy-api.sh` **不会** apply ConfigMap，须另跑 `kubectl apply -f k8s/minitube/configmap.yaml`（Pod 重建后变量才进进程）
- [ ] **不要**对已在跑的生产集群 `kubectl apply -f k8s/minitube/nfs-prep.yaml`：DaemonSet 重建会 `systemctl restart nfs-kernel-server`，生产 **hard** NFS 会卡住

### 构建产物

- [ ] 在能 `kubectl` 的机器上操作；`deploy-api.sh` 的 import 读的是 **k8s-master 宿主机**上的 `/tmp/minitube-images/minitube-api.tar`（或 `TAR_DIR`）。不在 master、路径也不共享时，改用 `build-images.sh` 的 HTTP import
- [ ] `GOPROXY` 可用（国内默认 `https://goproxy.cn,direct`）
- [ ] 除非 `SKIP_TEST=1`：本机 Postgres **5433** 起来，`cd api && go test ./internal/httpapi` 能过
- [ ] 将生成：`dist/minitube-api`（linux/amd64、`CGO_ENABLED=0`）、必要时 `dist/ffmpeg-bundle`、`minitube/api:local` tar

### 密钥边界

- [ ] `git status` 没有 `.env`、证书私钥、口令、Cloudflare token
- [ ] 示例只在 [`.env.example`](../.env.example)；真实值只在本机 `.env` 或 k8s Secret `minitube`
- [ ] 不把 `ADMIN_PASSWORD` / `JWT_SECRET` / `POSTGRES_PASSWORD` 写进 yaml 或 markdown

## 发布中

```bash
ENV=prod ./k8s/minitube/deploy-api.sh
# 跳过测试：SKIP_TEST=1 ENV=prod ./k8s/minitube/deploy-api.sh
# 测试环境：ENV=test ./k8s/minitube/deploy-api.sh
```

- [ ] 必须带 `ENV`，脚本不会默认打生产
- [ ] 脚本会记下旧 Running API Pod 名，再 `rollout restart`
- [ ] **hostPort 死锁**：新 Pod `Pending`、旧 Pod 仍 `Running` 时，只删**该 namespace 里重启前记下的那个旧 Pod**，不要循环删新 Pod（否则新实例起不来，Secret/配置也进不去）

## 发布后

### 服务就绪

细则与「healthz 证明不了什么」见 [ops.md](ops.md)。

- [ ] `kubectl -n minitube get pods -l app=minitube-api -o wide`：1 个 Running，节点 `k8s-master`
- [ ] 源站 `curl -sS -o /dev/null -w "%{http_code}\n" http://192.168.43.111:8080/healthz` → `204`（3s 内；`/healthz` 不应依赖 Postgres）
- [ ] 公网 `https://minitube.19121122.xyz/healthz` → `204`
- [ ] 公网首页 `https://minitube.19121122.xyz/` → `200`

### 关键路径（按本次改了什么勾）

- [ ] `GET /v1/public/site` 能返回 JSON（仍读库）
- [ ] 改了页面 / shorts：HTML 里 `?v=` 是新版本；打开对应路由手测
- [ ] 改了点播 / 转码：worker 仍 `Running`；上传一条短片能进 `processing` 并最终 `ready`（长片可能要很久，不要把发布页 `Failed to fetch` 当成转码失败）
- [ ] 改了直播：`rtmp://192.168.43.111:1935/live` 仍可推；HLS 能看
- [ ] 改了 ctc：页面仍走 Cloudflare；码流边缘 `192.168.43.111:18080`，见 [media-edges.md](media-edges.md)
- [ ] `/admin` 公网仍要口令 + TOTP；`/admin/setup` 仅局域网 `http://192.168.43.111:8080/admin/setup`

### 失败时

- [ ] 先看新/旧 API Pod 是否 Pending（hostPort）
- [ ] `kubectl -n minitube logs deploy/minitube-api --tail=80`
- [ ] 回退：检出上一发布提交，再跑一遍 `ENV=prod ./k8s/minitube/deploy-api.sh`（`:local` 会被覆盖；没有单独的 version tag）

清单走过发现问题就改本文，并在 PR 里说明。
