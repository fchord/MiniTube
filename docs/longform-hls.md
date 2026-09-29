# 长视频 HLS：MPEG-TS 存储 + 网页 TS→fMP4

约定：**磁盘与各端共用 MPEG-TS 分片**；Web（Chrome/Edge/Firefox）在浏览器里 **demux TS 再 remux 成 fMP4 喂 MSE**。不把对象存储改成 fMP4/CMAF。Safari / 原生 App 继续直接吃 TS。

短视频 WebCodecs 管线见 [`shorts-playback.md`](shorts-playback.md)，本文只管 **watch 长视频**。

## 目标

1. 一套码流：`hls/{id}/avc|{hevc}/{h}/seg_*.ts` + media/master `m3u8`。Flutter、其它 App、Safari 原生 HLS、未来机顶盒都拉同一套 TS。
2. Chromium 网页没有「`<video src=m3u8>` 可靠播 TS」这条路，必须走 **MSE + `video/mp4`**。因此网页多一次 **remux fMP4** 和 MSE 内部再 demux；相对解码，盒子开销有限。
3. 探测硬解（`VideoDecoder`）**不能**单独决定下发 HEVC。hls.js / MSE 问的是 **SourceBuffer 是否接受 `hvc1`/`hev1` 的 fMP4**。

## 谁走哪条播放路径

| 环境 | 容器 | 解码入口 |
| --- | --- | --- |
| Safari / iOS | MPEG-TS HLS | 原生 `video.src = m3u8`（不经过本文 remux） |
| Chrome / Edge / Firefox 播 **AVC** | TS →（hls.js 1.5）fMP4 | MSE `avc1`（现状保留） |
| Chrome / Edge / Firefox 播 **HEVC** | TS → **本仓库 `ts-fmp4.js`** → fMP4 | MSE `hvc1`（hls.js 1.5.18 的 MPEG-TS 文档只列 H.264） |
| Flutter / 原生播放器 | MPEG-TS HLS | 系统 demux，无 JS remux |
| 短视频 Web | TS | Mediabunny demux + `VideoDecoder`（另一套管线） |

HEVC 是否发给 Chromium：仅当 `MediaSource.isTypeSupported('video/mp4; codecs="hvc1…')`（或 Safari 原生 HLS + `canPlayType`）为真。Windows 上 MSE HEVC 往往还依赖系统 **HEVC 视频扩展**；那是解码器，不是 MSE API 本身。

## MPEG-TS / PES / NAL（实现必须遵守）

- TS 包 **188 字节**，`0x47`。视频 ES 被包头切开，JS 里 **gather 成连续缓冲无法避免**。
- `payload_unit_start_indicator` 只表示 **PES 开始**，不是 NAL 开始。
- **PES 头 16 位长度是 PES 载荷长度，不是 NAL 长度。** `PES_packet_length = 0` 仅视频允许，表示直到下一个 PES。
- **一个 PES 通常是一个 access unit（一帧），帧内多个 NAL**（AUD、VPS/SPS/PPS、SEI、一个或多个 slice）。不能「PES 长度 → 一个 NAL length」。
- **`PES_packet_length ≠ 0` 且帧 >64KB 时，一个 NAL 可以跨多个 PES。** 正确做法是：按 PID **顺序拼接所有视频 PES 载荷** 得到 Annex-B，再扫起始码切 NAL；用 PES 起始偏移给 NAL 赋 PTS。
- 起始码 `00 00 01` / `00 00 00 01`。4 字节起始码可原地改成大端 NAL 长度；3 字节不能。

## fMP4 片段（remux 产物）

不是「HEVC 前贴文件头、尾部再追加码流」。

- **Init（每个清晰度一次）**：`ftyp` + `moov`（`trak`/`hvc1` 或 `avc1` + `hvcC`/`avcC`，音频 `mp4a`+`esds`，`mvex`）。
- **每个 HLS `.ts` 分片 → 一个或多个 media fragment**：`moof`（`trun` 每帧 duration/size/CTS）+ `mdat`（8 字节盒头 + length-prefixed NAL 的 sample）。分片末尾无大 trailer。
- `hvc1` sample：参数集进 `hvcC`，sample 里是 length-prefixed VCL（及未抽走的 SEI）。本仓库转码 `-tag:v hvc1`、`-bf 0`、约 4s 关键帧，一帧一个 PES 是常见情况，实现仍按「多 NAL / 跨 PES」写。

## 额外代价（相对「内核直接解 TS」）

| 步骤 | 网页 Chromium | App / Safari |
| --- | --- | --- |
| TS demux（188→ES） | 有 | 内核做 |
| Annex-B → length-prefix + `moof`/`mdat` | **本文多出来的 remux** | 无 |
| MSE 再解析 fMP4 | **多一次 demux** | 无 |

`moof`+`mdat` 头通常 KB 级。大头仍是 gather ES 与解码。接受这笔网页税，换 **不改磁盘 TS**。

## Master `CODECS`

按 **该档分辨率** 写 HEVC level，禁止全表写死 `L120`（Level 4.0，不够 4K60）：

| 档位高度 | `CODECS` 视频部分 |
| --- | --- |
| ≤480 | `hvc1.1.6.L63.B0` |
| 720 | `hvc1.1.6.L93.B0` |
| 1080 | `hvc1.1.6.L123.B0` |
| 1440 | `hvc1.1.6.L150.B0` |
| ≥2160 | `hvc1.1.6.L153.B0` |

音频仍 `,mp4a.40.2`。AVC 最高 720，维持 `avc1.4d401f,mp4a.40.2`。

## 播放器行为

- 清晰度菜单 **只反映当前正在播的 master/levels**（AVC 回退后不得再列出 HEVC 的 2160p）。
- HEVC + `Hls.isSupported()`：走 `ts-fmp4.js`（MSE），失败再回退 `/avc/`。第一版 fragment **只含视频轨**（`addSourceBuffer` 不用 `mp4a`）；音轨 ADTS 仍在 TS 里，随后用第二条 SourceBuffer 接上。
- AVC：仍用 hls.js 1.5.18。
- 直播页暂不换引擎。

## 明确不做

- 转码输出改成 fMP4 分片作为唯一格式。
- 用 `VideoDecoder` 探测结果单独选择 HEVC 下发。
- 为减拷贝假设「一 PES 一 NAL」。
- 把 hls.js 升到 1.6/1.7 当作 HEVC-TS 的完整方案（完整构建才有 TS-HEVC，且仍进 MSE fMP4；本仓库 HEVC 点播用自研 remux）。
