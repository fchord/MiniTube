# 部署与运行环境

以仓库脚本和当前 homelab 实际操作为准。文档与脚本冲突时，改文档或改脚本并在 PR 里说明，不要留两套步骤。

公网站点：`https://minitube.19121122.xyz`（Cloudflare 回源 `192.168.43.111:8080`）。

## 前提

| 项 | 说明 |
| --- | --- |
| 开发机 | 能 `kubectl` 到本集群；能 `docker` 编镜像；Go 1.23+（编 API / worker） |
| `GOPROXY` | 国内默认 `https://goproxy.cn,direct` |
| 集群 | 三节点：`k8s-master` `192.168.43.111`、`k8s-worker2` `192.168.43.131`（NFS + GPU）、`k8s-worker1`（QSV，NotReady 时 worker 会 Pending） |
| 共享盘 | worker2 内核 NFS 导出 `/data/minitube`，PVC `minitube-media` 挂到 API `/data` |
| 镜像 | 不拉取 Docker Hub 的 ffmpeg。`bundle-ffmpeg.py` 把本机 `ffmpeg`/`ffprobe` 打进 `dist/ffmpeg-bundle`，再 `COPY` 进 `ubuntu:22.04` |
| 密钥 | 只放 k8s Secret / 本地 `.env`，**不要提交**。模板见 `.env.example`。`.gitignore` 已忽略 `.env`、`data/`、`dist/` |
| 本机端口 | compose Postgres 映射 **5433**；k8s API 用 master **hostPort 8080 / 18080**，与本机 `make run` 抢 8080 |

NVIDIA 用户态必须与内核模块同版本。worker2 若 `unattended-upgrades` 升了驱动未重启，NVENC Pod 会 CrashLoop（`Driver/library version mismatch`）。修法：重启该节点（NFS 会抖一下）。

## 本地开发

```bash
cp .env.example .env
make up          # postgres + SRS
make test        # 需要 5433 上的 Postgres
make run         # API :8080
make worker      # 另开终端；默认 libx264
```

公网播放把 `.env` 里 `PUBLIC_BASE_URL` 设为 `https://minitube.19121122.xyz`。

## 第一次切到集群

仓库根目录：

```bash
chmod +x k8s/minitube/cutover.sh k8s/minitube/build-images.sh
./k8s/minitube/cutover.sh
# 或 make k8s-cutover
```

脚本会：namespace + Secret → worker2 NFS 与节点 nfs-common → PVC / Postgres / ConfigMap → 若本机 compose Postgres 可达则 dump 进 StatefulSet → 把 `api/data` 拷到 NFS → `build-images.sh` 编三种镜像并 `ctr import` → 停本机 8080/1935 → 拉起 API / SRS / GPU worker → 打 `http://127.0.0.1:8080/healthz`。

`cutover.sh` 写入的 Secret 字段是 `POSTGRES_PASSWORD`、`JWT_SECRET`、`SRS_HOOK_SECRET`。站点 `/admin` 首次种口令还需要 `ADMIN_PASSWORD`（只写 Secret，不要进 git）：

```bash
kubectl -n minitube patch secret minitube --type merge \
  -p '{"stringData":{"ADMIN_PASSWORD":"<自行生成的口令>"}}'
kubectl -n minitube rollout restart deploy/minitube-api
```

种进库之后改口令走局域网维护页，不必再改这条环境变量。

直播推流：`rtmp://192.168.43.111:1935/live`。码流边缘 TLS：`192.168.43.111:18080`，见 [media-edges.md](media-edges.md)。

## 日常只更新 API 镜像

API 只跑在 `k8s-master` 的 `hostPort: 8080`。日常改页面/引擎用下面脚本（含测试、import、重启、探活），不要只写 `rollout restart` 却忘了先把新镜像打进 containerd。

**import 路径假设：** `deploy-api.sh` 在 k8s-master 上用 `ctr import /tmp/minitube-images/minitube-api.tar`（Job `nsenter` 读的是 **master 宿主机**上的这个路径）。请在 **k8s-master**（或与 master **共享该 tar 路径**的机器）上执行。若在别的机器编镜像，不要用这条本地路径，应复用 `build-images.sh`：master 起 HTTP 提供 tar，其它节点 `curl | ctr import`。

```bash
chmod +x k8s/minitube/deploy-api.sh
./k8s/minitube/deploy-api.sh
# 跳过测试：SKIP_TEST=1 ./k8s/minitube/deploy-api.sh
```

等价手工步骤：

1. `cd api && go test ./internal/httpapi`（需能连 `DATABASE_URL`，默认 `127.0.0.1:5433`）
2. `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/minitube-api ./cmd/api`
3. 若没有 `dist/ffmpeg-bundle`，跑 `python3 k8s/minitube/bundle-ffmpeg.py`
4. `docker build -f k8s/minitube/Dockerfile.api -t minitube/api:local .`
5. `docker save minitube/api:local -o /tmp/minitube-images/minitube-api.tar`
6. 特权 Job：`nsenter` + `ctr -n k8s.io images import` 打进 **k8s-master**
7. `kubectl -n minitube rollout restart deploy/minitube-api`
8. **hostPort 死锁**：新 Pod `Pending`、旧 Pod 仍 `Running` 时，只删**重启前记下的那个旧 Pod**，不要循环删新 Pod
9. 验证：`http://192.168.43.111:8080/healthz` 与 `https://minitube.19121122.xyz/healthz` 为 **204**；若改了嵌入页，看 HTML 里 `?v=` 是否新版本

同时更新 worker 镜像才跑 `./k8s/minitube/build-images.sh`，再 `kubectl -n minitube rollout restart deploy -l app=minitube-worker`。

## 站点管理

- 公网 `/admin`：口令 + TOTP；登录按 IP 限频（每分钟最多 8 次，连续 3 次错误锁定）。
- `/admin/setup`（改口令、绑/换 Authenticator）**只允许局域网** `http://192.168.43.111:8080/admin/setup`。经 Cloudflare 或公网域名访问为 404。
- 管理页可写 `shorts_engine`（`legacy` / `webcodecs`）和 ctc 码流分流开关，热生效，见 [media-edges.md](media-edges.md)。

## 探活

`minitube-api` 的 readiness 打 `/healthz`，超时 3s。该路径不应依赖 Postgres。页面 HTML 不经库；`GET /v1/public/site` 仍读库。

## 验收（部署本身）

- `curl -sS -o /dev/null -w "%{http_code}\n" http://192.168.43.111:8080/healthz` → `204`
- 公网首页 `200`
- 若改了 shorts/页面：对应 `?v=` 与手测路径通过
