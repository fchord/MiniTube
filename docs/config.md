# 环境变量清单

加载方式：

| 场景 | 来源 |
| --- | --- |
| 本机 `make run` / `worker` / `test` | 仓库根 `.env`（从 [`.env.example`](../.env.example) 复制） |
| API / worker 进程 | `api/internal/config.FromEnv` 以及 worker/转码里的 `os.Getenv` |
| 集群 | ConfigMap `minitube`、Secret `minitube`；`DATABASE_URL` 在容器启动命令里用 `POSTGRES_PASSWORD` 拼出来 |
| 切流脚本 | `cutover.sh` / `build-images.sh` 读 `.env` 或默认值 |

真实口令、JWT、证书私钥只放 `.env` 或 Secret，**不要提交**。`.gitignore` 已忽略 `.env`（保留 `.env.example`）。

下面「缺省」指代码/脚本在变量为空时的回退，不是生产建议值。

## API

| 变量 | 缺省 | 用途 |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | HTTP 监听 |
| `DATABASE_URL` | `postgres://minitube:minitube@127.0.0.1:5433/minitube?sslmode=disable` | Postgres。本机 compose 映射 **5433**；集群由启动脚本写成 `minitube-pg:5432` |
| `JWT_SECRET` | `dev-change-me` | 访问令牌签名（**密钥**） |
| `PUBLIC_BASE_URL` | `http://127.0.0.1:8080` | 对外媒体/链接基址。公网站点用 `https://minitube.19121122.xyz` |
| `UPLOAD_DIR` | `./data/uploads` | 上传与点播文件。集群 ConfigMap 为 `/data/uploads` |
| `RTMP_BASE_URL` | `rtmp://127.0.0.1:1935/live` | 创建直播时下发的推流前缀。集群为 `rtmp://192.168.43.111:1935/live` |
| `SRS_HLS_DIR` | `./data/srs-hls` | SRS HLS 落盘。集群 `/data/srs-hls` |
| `LIVE_HLS_DIR` | `./data/live-hls` | 直播 origin HLS。集群 `/data/live-hls` |
| `SRS_HOOK_SECRET` | `dev-srs-hook` | SRS `on_publish` 等回调校验（**密钥**） |
| `LIVE_IDLE_TTL` | `1h` | 推流停且无拉流后标 ended 的空闲时间 |
| `ACCESS_TOKEN_TTL` | `168h` | 登录 access token |
| `REFRESH_TOKEN_TTL` | `720h` | refresh token |
| `EMAIL_TOKEN_TTL` | `24h` | 邮箱验证令牌 |
| `ADMIN_PASSWORD` | 空 | 仅当库中尚无 `admin_account` 时种站点管理口令（**密钥**）。空则不种 |
| `ADMIN_USER_IDS` | 空 | 可用用户 JWT 调 `PATCH /v1/admin/site` 的 uuid，逗号分隔 |
| `MEDIA_EDGE_TLS_ADDR` | 空 | 非空则另听 HTTPS（集群 `:18080`）。空则不听 |
| `MEDIA_EDGE_TLS_CERT` | `/data/ctc-tls/fullchain.pem` | 边缘 TLS 证书 |
| `MEDIA_EDGE_TLS_KEY` | `/data/ctc-tls/privkey.pem` | 边缘 TLS 私钥路径（**密钥文件**） |
| `MEDIA_EDGE_TLS_HOST` | `ctc.minitube.19121122.xyz` | 边缘证书 Host / SNI |

## Worker / 转码

| 变量 | 缺省 | 用途 |
| --- | --- | --- |
| `TRANSCODE_ENCODER` | `libx264` | `h264_nvenc` / `h264_qsv` / `h264_vaapi` / `libx264` |
| `TRANSCODE_PRESET` | `veryfast`（未设时）；k8s NVENC 常用 `hq` | ffmpeg preset |
| `TRANSCODE_RANK` | `0` | 抢任务优先级，k8s 为 1–4 |
| `TRANSCODE_MAX_HEIGHT` | `1080` | 转码最高档 |
| `TRANSCODE_WORKER_ID` | 主机名 | 工作者 id；k8s 用 Pod 名 |
| `TRANSCODE_JOB_TIMEOUT` | 空=按片源估时（有上下限） | 可选上限；合法 duration 且小于估时才生效 |
| `VAAPI_DEVICE` | `/dev/dri/renderD128` | VAAPI 设备节点 |

集群 worker 另有 `NVIDIA_VISIBLE_DEVICES`、`NVIDIA_DRIVER_CAPABILITIES`（仅 NVENC Deployment），不进 `.env.example`。

## 脚本与集群 Secret

| 变量 | 缺省 | 用途 |
| --- | --- | --- |
| `GOPROXY` | `https://goproxy.cn,direct` | `build-images.sh` / `deploy-api.sh` |
| `MASTER_IP` | `192.168.43.111` | 切流、HTTP 传镜像、提示推流地址 |
| `NFS_SERVER` | `192.168.43.131` | `cutover.sh` showmount；ConfigMap 里也有同名项 |
| `POSTGRES_PASSWORD` | `minitube` | compose / `cutover.sh` 建 Secret（**密钥**） |

k8s Secret `minitube` 现用字段：`POSTGRES_PASSWORD`、`JWT_SECRET`、`SRS_HOOK_SECRET`，以及可选 `ADMIN_PASSWORD`。其它 API 项在 ConfigMap `minitube`。
