# 生产与测试环境

同一套三节点 k8s，两套工作负载。本机 `make run` 仍是第三套（开发）。

| | 开发（本机） | 测试 | 生产 |
| --- | --- | --- | --- |
| 域名 | `http://127.0.0.1:8080` | `https://minitube-test.19121122.xyz` | `https://minitube.19121122.xyz` |
| Cloudflare 回源 | 无 | `192.168.43.111:8081`（橙云，独立主机名） | `192.168.43.111:8080` |
| namespace | — | `minitube-test` | `minitube` |
| API 镜像 | 源码 | `minitube/api:test` | `minitube/api:local` |
| API 端口 | 本机 8080 | master **hostPort 8081** | master **hostPort 8080** + **18080**（ctc） |
| Postgres | compose **5433** | STS，hostPath `/data/minitube-pg-test` | STS，hostPath `/data/minitube-pg` |
| 媒体盘 | `./data` | NFS `/data/minitube-test` | NFS `/data/minitube` |
| 直播 SRS | compose 1935 | master **hostPort 1936** | master **hostPort 1935** |
| 转码 | `make worker` CPU | worker2 NVENC（连测试库；不独占 `nvidia.com/gpu`） | worker2/master NVENC + QSV |
| ctc | 本机一般关 | **不做**。`APP_ENV=test` 强制关，`/admin` 选项灰掉「测试环境暂不支持」 | `/admin` 可开，见 [media-edges.md](media-edges.md) |
| `APP_ENV` | `dev`（缺省） | `test` | `prod` |
| Secret | `.env` | Secret `minitube`（**另一套** JWT / 库口令 / hook） | Secret `minitube` |

DNS / Cloudflare 记录由人加；仓库不写账号 token。测试域名已指向 8081 后即可用 `https://minitube-test.19121122.xyz/healthz`。

## 第一次拉起测试环境

在能 `kubectl` 且与 k8s-master **共享** `/tmp/minitube-images` 的机器上：

```bash
chmod +x k8s/minitube-test/bootstrap.sh k8s/minitube/deploy-api.sh
./k8s/minitube-test/bootstrap.sh
```

脚本会：在 worker2 上导出 `/data/minitube-test`（不动 `/data/minitube`）→ namespace / ResourceQuota / LimitRange / 独立 Secret → PVC / Postgres / API / SRS:1936 / NVENC worker → `ENV=test` 编 `minitube/api:test` 并 import → 探活 `:8081`。`limits.cpu` 由 LimitRange 补默认值，否则配额会拒建 Pod。

种管理口令（不要用生产口令）：

```bash
kubectl -n minitube-test patch secret minitube --type merge \
  -p '{"stringData":{"ADMIN_PASSWORD":"<自行生成的口令>"}}'
kubectl -n minitube-test rollout restart deploy/minitube-api
```

局域网维护页：`http://192.168.43.111:8081/admin/setup`。测试推流：`rtmp://192.168.43.111:1936/live`。

## 日常更新 API

**必须带 `ENV`**，脚本不会默认打生产：

```bash
ENV=prod ./k8s/minitube/deploy-api.sh    # 生产 8080，镜像 :local
ENV=test ./k8s/minitube/deploy-api.sh    # 测试 8081，镜像 :test
# 或 make deploy-api ENV=prod
```

import 路径假设与生产相同：Job `nsenter` 读 **master 宿主机**上的 `TAR_DIR/minitube-api.tar`。hostPort 死锁时只删**该环境**里重启前记下的旧 API Pod。

同时更 worker 镜像仍用 `./k8s/minitube/build-images.sh`，再按 namespace 重启：

```bash
kubectl -n minitube-test rollout restart deploy/minitube-worker-nvenc-w2
```

## 不要做的事

- 不要把测试回源指到 8080，或把生产 `PUBLIC_BASE_URL` 改成 test 域名
- 不要对 `minitube-test` 跑 `cutover.sh`（会动生产 Secret / 8080 / 1935）
- 不要把生产库 dump 进测试库（用户、TOTP、直播密钥）
- 测试不要听 18080，不要复用 `ctc.minitube.19121122.xyz`

编排清单在 [`k8s/minitube-test/`](../k8s/minitube-test/)。变量见 [config.md](config.md)。
