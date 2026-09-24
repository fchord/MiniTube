# 短视频滑动播放

本文记录 `/shorts`、`/shorts/{videoId}` 的队列、交互，以及播放引擎约定。`videoId` 为 10 位公开编号（旧 UUID 仍可打开）。实现以本文为准。个性化推荐以后只替换发现页排序，不改「前端持有本次 playlist」的形态。

相关代码：`api/internal/httpapi/shorts.html`、`shorts-engine.js`、`shorts-worker.js`（demux）、`shorts-decode-worker.js`、`shorts-hwtest.html` / `shorts-hwtest.js`、`GET /v1/feed/shorts`、`GET /v1/public/site`。契约见 [`openapi.yaml`](openapi.yaml)。

## 原始需求与对齐

讨论时的六条目标，以及后来改掉的点：

1. **播放队列。** 进页后有一条逻辑上可循环的队列。频道页点进去 = 该频道当前可见短视频；其它入口 = 从公开短视频随机填。
2. **预加载并解码。** 当前第 `i` 条时，正向再准备 `k` 条，负向保留已刷过的。`k` 初值 3。拍板后负向只留 1 路。
3. **刷出窗口即拆掉** 解码器和帧缓存；刷回来从离开的时间点重新解、续播。
4. **播到末尾 loop**，不自动切下一条。
5. **声音。** 最初希望进页带声。Chrome / Firefox（桌面和手机）的自动播放政策都会拦「有声自动播放」，不是 Android 独有。只要有一端做不到默认有声，全端 **默认静音**；右侧静音按钮同步该状态，用户只通过按钮（或键盘 `M`）开/关声音。
6. **不要** 页面提示「滚轮 / ↑↓」。

hls.js + `<video>.play()` 无法做到「后台预解码 YUV/PCM、刷上来零延迟渲染」，因此增加 **WebCodecs 自研管线**，用站点配置在旧引擎和新引擎之间切换。

## 已拍板（产品与队列）

| 项 | 约定 |
| --- | --- |
| 队列持有 | 前端本次页面持有 playlist。后端只提供取片接口，不建服务端 session 队列。 |
| 频道入口 | `/shorts/{id}?ch={handle}`。队列 = 该频道当前可见短视频，顺序与频道页网格一致，从点进去的那条开始。列表绕圈。 |
| 其它入口 | `/shorts` 或 `/shorts/{id}` 无 `ch`。当前片（若有）放最前，其余公开短视频随机去重。先补新片；补不动再绕本会话。 |
| 未来推荐 | 发现页仍是「给当前用户下一页」。现在 `random=1`，以后同一接口换排序。 |
| 播完 | 本片 loop，不自动切下一条。 |
| 声音 | **默认静音**。静音按钮是唯一开关（键盘 `M` 与按钮相同，并闪 HUD）。点画面 / 空格 / 中间三角 **只** 播放暂停，不开声音。 |
| 画面点击 | PC（细指针）：单击画面切换播放/暂停。手机（`hover: none` 且 `pointer: coarse`）：单击唤出/收起中间播放暂停按钮；点中间三角才切换。进页不显示三角。 |
| 进度 | 底边 3px 条 `#3ea6ff`，只读，不可拖。 |
| 预加载窗口 | 负向固定 **1** 路；当前 1 路；正向目标 **k=3**。列表 DOM 与硬解窗口对齐为 **1+1+3**。硬解不够则只降正向 k，不做网页软解。 |
| 提示 | 不显示「滚轮 / ↑↓」。键盘上下、空格、`M` 可保留。 |

竖滑过渡（键盘 \(e(p)\)、手指甩、未做回弹）见 [`shorts-slide.md`](shorts-slide.md)。

### 播放页交互（实现约束）

- 竖滑：`#feed` 上 `setPointerCapture`。控件（`.mute` / `.like` / `.bigplay` / 链接 / 顶栏）不进入拖动手势。Chrome 上 canvas/video 会抢走命中，因此画面层 `pointer-events: none`，并用 `hitUiControl` 按按钮矩形判断。
- 未滑动的 pointerup：PC 上 `togglePlayback`；粗指针上只切 chrome。捕获后的 `click` 可能落到 `#feed`，同样走 `hitUiControl`。
- 不要在 `pointerdown` 里重写静音按钮 `innerHTML`（引擎每次按下都会 `unlockAudio`）。图标仅在 `muted` 真变时更新，否则 Chrome 会丢掉这次 click。
- `isSilent()` / `AudioContext.state` 能反映「想有声但被拦」，**不**用来改播放暂停，也 **不** 覆盖用户按钮状态。

### 为什么队列状态放前端

个性化需要的是「这个用户下次该看到谁」，不是「服务器替这个标签页记一份 list」。刷新、多端、分享 `/shorts/{id}` 都会和 Redis session 打架。发现接口保持分页即可。

频道队列走 `GET /v1/channels/{handle}/videos?kind=short`，入口必须带 `ch=`。频道页一次最多 50 条。

同一路流（同一个 video id 的这一次窗口占用）只开一对音视频解码器，不为绕圈把同一 id 挂两套。

## 播放引擎切换

| 项 | 约定 |
| --- | --- |
| 配置 | Postgres `site_settings` 一行 `shorts_engine` = `legacy` \| `webcodecs`。改完立刻生效，不必重启 Pod。可在 `/admin` 切换。 |
| 读取 | `GET /v1/public/site`（未登录可读）。缺省 **legacy**。 |
| 写入 | `PATCH /v1/admin/site`：站点管理会话（口令+TOTP，见 `/admin`），或 user id 在 `ADMIN_USER_IDS` 中的账号。 |
| 覆盖 | 本页 `?engine=legacy\|webcodecs` 只影响当前标签，方便对照，不写库。 |
| 能力 | 即使配置是 webcodecs，没有 `VideoDecoder` / `AudioDecoder` 也走 legacy。 |
| 失败 | 第一版新引擎失败要看得见，不悄悄切回 hls.js。 |

`legacy`：hls.js + `<video>`（现有页）。  
`webcodecs`：下文自研管线。长视频 / 直播仍用 hls.js + `player.js`。

## 自研管线（webcodecs 第一版）

```
主线程：滑动 DOM、按钮、rAF 从 deque 取帧 → Canvas 2D drawImage；单墙钟（音频 `set`，视频跟踪）。竖滑 \(e(p)\) 见 [`shorts-slide.md`](shorts-slide.md)；AV 同步见 [`shorts-av-sync.md`](shorts-av-sync.md)；PCM 不足时攒码流见 [`shorts-rebuffer.md`](shorts-rebuffer.md)。
每路两个 Worker（进入 1+1+3 硬解窗口才起，离开 terminate）：
  demux Worker：拉 HLS、Mediabunny 解 MPEG-TS，把编码包送进 packet pipe
  decode Worker：VideoDecoder / AudioDecoder，把帧送进 frame pipe
AudioWorklet：把已到点的 PCM 送给扬声器
```

拉流和解码都不在主线程。主线程不解码，只合成。解码窗口内每一路独占 **demux + decode** 两条 Worker，互不等待时间片；packet / frame 管道按原来的秒数上限尽力填满，满了才 sleep 再查。

| 项 | 约定 |
| --- | --- |
| 解封装 | Mediabunny，文件在 `/static/`，仅 webcodecs 时 `import()` |
| 分片 | 维持现有 HLS + MPEG-TS |
| 档位 | 只播 master 里 **最高** 的视频档 |
| 视频编码 | 只保证 **H.264**。遇到 HEVC/AV1 当这条失败 |
| 音频编码 | 只保证 **AAC** |
| 鉴权 | 新引擎拉媒体 **不带** Bearer / Cookie |
| deque | 每一路独立的 video deque、audio deque，目标约 **500ms** |
| 背压 | 解码器是生产者，渲染是消费者。packet pipe 满则 demux 停 `next()`；frame pipe 满则停 `decode()` |
| 时钟 | **单墙钟**，音频为基准。听路视频约 0.3s、音频约 0.6s 都进 deque 后，先把扬声器 FIFO 填到约 400ms，再用放音点 \(a_{\mathrm{play}}\) 开钟（`clock-start` / `pcm-start`）。视频跟 \(\mathrm{clock}\)（TryToMatch / LostMatch），不 `set` 钟。片源 loop 用绝对 PTS 续走（不重设墙钟）；标签回前台暂停/继续钟。FIFO 将空时进入攒码流（冻钟、Worklet `hold`），见 [`shorts-rebuffer.md`](shorts-rebuffer.md)。细则见 [`shorts-av-sync.md`](shorts-av-sync.md)。 |
| loop | 解到末尾后从码流头继续解 H.264/AAC，往同一 deque 接着填，不丢已经在环里的帧 |
| 窗口外 | 拆 Mediabunny、拆 codec、拆两个 deque，所有 `VideoFrame` / `AudioData` `close()` 掉。记下 pts。回窗口从 pts 前关键帧重新解 |
| 切源音频 | 页面生命周期尽量一个 `AudioContext`；PCM 重采样到 `context.sampleRate`（跨帧连续相位）。扬声器走 **AudioWorklet** FIFO（`ScriptProcessor` 仅作加载前垫片），预填约 **400ms**，不按墙钟逐帧 `due` 才送。Context `suspended` 时仍按页面 `muted` 显示按钮，用户点按钮再 `unlockAudio` / `setMuted(false)` |
| 开声 | 默认 `muted`。按钮把用户意图写进引擎 `setMuted`；听路与 legacy `<video>` 都跟这个标志 |
| 硬解不够 | 无标准 API 可读上限。`configure`/`decode` 报错，或 **300ms 解不出第一帧** → 减正向 k（最低 1）。听路未攒够约 0.3s 视频 + 0.2s 音频前不开邻路硬解。起播出声另等 0.6s 音频，两道闸不要混用。 |

三层窗口不要并成一层：

| 层 | 范围 | 做什么 |
| --- | --- | --- |
| 列表 `keepIds` | 负向 1 + 当前 + 正向 k=3 | 只留 DOM / 画布 / 链接。 |
| 硬解 `decodeIds` | 同上 **1+1+3**（`fwdK` 可降到 1） | 该路起 demux + decode Worker。引擎 `setHotWindow(prev, fwdIds)`，`warms` 最多 3 个前向 id。邻路只 snap 第一帧到 canvas，GOP 后续帧留在 deque。满窗最多 5 路 × 2 Worker。 |
| 管道填满 | 每路自己尽力 | packet pipe 上限仍是约 3s / `VPD_MAX`；frame pipe 听路视频约 0.5s+slack、音频约 0.85s+slack，邻路封面 GOP 后约 0.25s。邻路视频时间轴超过约 2s 不再 demux。听路且音频还有 credit 时 demux **先拉 AAC**。满了 sleep，没满继续干活。 |

封面 snap 只画第一帧；GOP 里随后解出的帧留在 deque 里给起播用，按 pts 排序。切到该条时封面停住，仍等听路视频约 0.3s、音频约 0.6s 再 `clock-start` / `pcm-start`。不要把墙钟跳到后面的 P 帧上。

Mediabunny 只当 HLS+TS 解包器（`EncodedPacketSink`）。解码器由我们 `new VideoDecoder` / `AudioDecoder`。渲染用 Canvas 2D `drawImage(VideoFrame)`，不把 YUV 平面丢给 2D context。

## 接口

发现页：

```
GET /v1/feed/shorts?random=1&limit=8&exclude={id,id,...}
```

站点：

```
GET /v1/public/site
PATCH /v1/admin/site   { "shortsEngine": "webcodecs" }
```

频道队列：`GET /v1/channels/{handle}/videos?kind=short&limit=50`。

## 硬解路数测试页

`/shorts/hwtest`（`shorts-hwtest.html` / `shorts-hwtest.js`）与播放页窗口无关，用来压 GPU。当前：

| 项 | 约定 |
| --- | --- |
| 布局 | 5×2，格宽约 84%，`SLOTS = 10` |
| 片源 | 频道 `demo1` 的 short |
| 音频 | `dropAudio`，不建扬声器 |
| 渲染 | `DRAW_EVERY_MS = 1000`，每路约 1fps 画 canvas，少占合成 |
| 计数 | 格内 fps 按 **解码帧** 计，不是上屏帧 |
| 对齐 | 画面相对 vclock 差过约 1s 才 `set` 钟（`SNAP_SEC`），避免 drop 旧帧后快进 |

本机参考（GTX 960、硬解 H.264）：4×1080p + 6×720p 共 10 路，其中 2 路 60fps、8 路 30fps，任务管理器 VideoDecode 约 48% 时仍可同时开 10 路。播放页因此把硬解从 1+1+1 扩到 1+1+3。

## 可能存在的问题

这些在第一版里就会碰到，需要盯，但不阻塞开工。

1. **AudioContext 自动播放。** 没有用户手势时 context 常是 `suspended`。已接受默认静音；点静音按钮才 `resume`。不要把「第一次点画面」兼作开声（会和 PC 单击暂停打架）。
2. **墙钟 vs 音频设备钟。** 画面跟 `performance.now()`，扬声器跟 DAC。短片上漂移通常不大；后台回来、蓝牙耳机切换可能对口型。
3. **硬解路数不可探测。** 只能用报错和 300ms 无帧减 k。不同 GPU/浏览器上限差很多，有的机能开 5 路，有的开到第 2 路就黑。
4. **300ms 误伤。** 弱网先拉清单再解，首帧超过 300ms 会被当成硬解失败而减 k。
5. **Worker 里的 VideoFrame 转到主线程。** 必须 `postMessage(..., [frame])` 转移所有权；漏 `close()` 会撑 GPU。Safari 对可转移 `VideoFrame` 可能比 Chrome 脆。
6. **Canvas 2D 只吃 8bit 上屏。** 当前 H.264 8bit 没问题；以后 10bit/HDR 会发灰或被色调映射。
7. **MPEG-TS 时间基。** PCR/PTS 折成微秒若有偏差，loop 重设时钟可遮一部分；切档（第一版不做 ABR）时会暴露。
8. **Annex-B 与 `avcC`。** Mediabunny 的 `getDecoderConfig()` 若和浏览器期望不一致，会 `configure` 失败。TS 比 fMP4 更容易踩这个。
9. **无鉴权。** 公开片可以；私藏短视频新引擎拉流会 403。频道主刷自己的私藏可能只能走 legacy。
10. **最高档。** 手机上第一口就是最高分辨率，硬解更热、首帧更慢，和「轻、快」有张力。
11. **标签页后台。** `rAF` 停、墙钟不停，回来会按「落后太多」丢光 deque。已规定回前台重设时钟，仍可能闪一下。
12. **Mediabunny 把 HLS 当成一个大文件。** 可能比 hls.js 多拉分片，或在 seek/loop 时行为与「只保留 500ms」不完全一致。库较新，HLS+TS 边角（断档、DISCONTINUITY）未在短视频 VOD 上验证。
13. **每路双 Worker。** 1+1+3 最多 10 条 JS Worker 再加 AudioWorklet；弱机可能更热。硬解失败只降正向 k。packet/frame 满了才 sleep，不再用全局 `pickWork` 互让。
14. **无 WebCodecs 的壳。** 微信/旧 WebView 会静默落到 legacy，两端体感不一致。
15. **loop 接缝。** 缓冲若盖不住「回到 IDR 再解出帧」的间隙，会进攒码流停一拍，见 [`shorts-rebuffer.md`](shorts-rebuffer.md)。
16. **主线程 rAF 仍可能因 JS 忙而丢绘制。** 解码在 Worker，但 `drawImage` 在主线程；滑动 DOM 重时仍可能掉帧。
17. **网/demux 跟不上。** 已改为 FIFO 低水位冻钟再播，避免补 0 刺啦；听路先拉 AAC。弱网仍会偶发停顿，这是预期。

## 需要后续再完善的问题

第一版明确不做，记在这里以免忘掉。

1. 运营开关页（现在靠 SQL 或 `PATCH /v1/admin/site`）。
2. ~~第一次任意手势静默开声。~~ 已否：点画面必须只负责播放/暂停。开声只走静音按钮 / `M`。
3. 切源重建 AudioContext 导致再次 mute 的自动恢复。
4. 新引擎单条失败后回退该条 hls.js（现在要看得见失败）。
5. ABR / 按网速选档（现在固定最高档）。
6. HEVC、AV1；软解 polyfill。
7. 新引擎拉流带鉴权，私藏可播。
8. 探测或协商硬解路数；按机型设 k 上限。
9. 已做：解码放到每路独立 demux/decode Worker。后续可再试 OffscreenCanvas 在 Worker 里画，进一步卸主线程。
10. DAC / 蓝牙延迟补偿仍未做。墙钟 AV 策略见 [`shorts-av-sync.md`](shorts-av-sync.md)。
11. 10bit/HDR：WebGPU `importExternalTexture`。
12. 频道短视频超过 50 条的完整翻页绕圈。
13. 发现页个性化排序（只换 feed 实现）。
14. HLS 分片是否改为 fMP4/CMAF（上 AV1 或解 TS 摩擦过大时再议）。
15. 把短视频播控和长视频 `player.js` 合成一套（不适合，交互不同）。

## 非目标（本阶段不做）

- 服务端为一次观看持久化 playlist / Redis session
- 网页软解凑硬解
- 为开声音改成无刷新 SPA
- ffmpeg.wasm 当播放引擎
- 用 mpegts.js / hls.js 当 WebCodecs 的 demux 出口（公有 API 对不齐）
- Mediabunny `CanvasSink` 当滑动播放器
