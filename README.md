# MiniTube

全平台视频站：点播 + 直播为主，图文帖为辅。

客户端策略：**统一 API + Flutter（Android / iOS / iPadOS / Windows / macOS / Linux）+ 独立 Web 主站**。订阅对象是**频道**，不是用户。

当前进度：**Phase 5（直播）**。API 语言为 **Go**。

## 本地运行

```bash
cp .env.example .env
# 公网播放把 PUBLIC_BASE_URL 改成 https://minitube.19121122.xyz
make up
make run      # API :8080
make worker   # 另开一个终端，FFmpeg 转码
```

进现有 k8s（含 GPU 硬编）见 [k8s/minitube/README.md](k8s/minitube/README.md)，一条命令：`./k8s/minitube/cutover.sh`。

## 仓库布局

| 目录 | 用途 |
| --- | --- |
| [docs/](docs/) | 领域模型、路线图、OpenAPI、DDL |
| [api/](api/) | Go 业务 API |
| [web/](web/) | 独立 Web 主站（SEO / 分享 / 嵌入） |
| [app/](app/) | Flutter 多端应用 |
| [k8s/](k8s/) | 集群：GPU plugin 与 MiniTube 工作负载 |

## 分期（摘要）

1. Phase 1：身份与频道
2. Phase 2：长视频点播
3. Phase 3：社交帖
4. Phase 4：短视频
5. Phase 5：直播 ← **进行中**
6. Phase 6：搜索 / 推荐 / 审核 / 嵌入

详见 [docs/roadmap.md](docs/roadmap.md)。
