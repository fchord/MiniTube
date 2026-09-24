# Shorts 攒码流（PCM 不足时停钟再播）

本文记录 `/shorts` webcodecs 引擎在 **扬声器即将断流** 时如何进入「攒码流」、停住呈现、等 PCM deque 够了再恢复。实现以 [`shorts-engine.js`](../api/internal/httpapi/shorts-engine.js)、[`shorts-worklet.js`](../api/internal/httpapi/shorts-worklet.js)、[`shorts-worker-src.js`](../api/internal/httpapi/shorts-worker-src.js)、[`shorts-decode-worker.js`](../api/internal/httpapi/shorts-decode-worker.js) 为准。队列与硬解窗口见 [`shorts-playback.md`](shorts-playback.md)；墙钟与 TryToMatch 见 [`shorts-av-sync.md`](shorts-av-sync.md)。

## 要解决的现象

网或 demux 跟不上实时播放时，Worklet `process()` 仍按 DAC 节拍要 PCM。队列空了就补 0，听感是 **刺啦刺啦**（频繁断流），不是干净的「停一下」。

错误做法：回调已经饿了再往 FIFO 里补——这一量子的 0 已经送进 DAC。正确做法：在 FIFO 低水位时 **停钟、停送 PCM/YUV、Worklet 不再消费**，让 Mediabunny / demux / decode 继续填；PCM deque 够了再恢复。画面停在当前帧，进度条（`shownPts`）也停。

这不是用户暂停，也不是切后台。UI 仍是播放中。

## 三层音频缓冲

不要把「PCM 不足」理解成单一队列。听路音频有三层，断流位置不同，对策也不同。

```
Mediabunny 拉 HLS/TS
        ↓
demux Worker：AAC 包 deque（apd / aps）
        ↓
decode Worker：AudioDecoder → 主线程 PCM deque（st.audio）
        ↓  pumpSpeaker
AudioWorklet FIFO（fifo / workletQs）
        ↓  process()
扬声器
```

| 符号 | 是什么 | 谁填 | 谁吃 |
| --- | --- | --- | --- |
| `aacS` / `aacN` | 尚未 `decode()` 的 AAC 包秒数 / 个数 | demux | decode |
| `pcmS` / `pcmN` | 已解码、尚未泵进 Worklet 的 PCM | decode → 主线程 | `pumpSpeaker` |
| `fifo` | Worklet 里真正在响的交错立体声 | `pumpSpeaker` | `process()` |
| `adq` | `AudioDecoder.decodeQueueSize` | decode 已 submit | 解码器吐帧 |

主线程卡顿、decode 慢、网断，在日志里长得不一样：

| `fifo` | `pcmS` | `aacS` | 含义 |
| --- | --- | --- | --- |
| 0 | 很大 | 任意 | 主线程没泵，不是缺码。应继续 `pumpSpeaker`，**不要**攒码流 |
| 0 | 0 | 很大 | 解码没跟上 |
| 0 | 0 | 0 | 上游没包：网或 demux（含视频 `next()` 堵住音频） |

实测卡顿（`tick` 仅 0.3ms、`n` 上千、`got` 几乎不涨）属于第三行。墙钟若空转，随后会出现 `lost-match`（画面 PTS 超前钟约 2s）。所以攒码流必须 **冻 clock**，不能只停泵。

## 状态机

标志位 `rebuffering`，与 `paused`、`visHold` 并列。

```
播放中
  │  Worklet `low`（FIFO ≲ 80ms）或 `underrun` / ScriptProcessor 饿回调
  │  且 pcmS < 80ms
  │  且已 startArmed、spkLive、非暂停、非后台、非 noAudio
  ▼
enterRebuffer
  clock.pause()          冻媒体时间（复用 createClock 的 held，不新接口）
  worklet post "hold"    playing=false，FIFO 不清、不 reset
  停 pumpSpeaker
  停 takeVideoOne        画面停在当前帧
  demux/decode 继续      reportHeld 照常，背压仍在
  │
  │  最短冻结 REBUFFER_MIN_MS = 180ms（避免一顿一顿）
  │  pcmS − D0 ≥ REBUFFER_GAIN(0.5s)  或  pcmS ≥ REBUFFER_HIGH(0.6s)
  ▼
leaveRebuffer
  clock.play()           若用户未暂停、未 visHold
  pumpSpeaker            先把 FIFO 填到 PCM_AHEAD
  worklet post "start"   即使 spkLive 已是 true 也要再发（hold 之后必须二次 kick）
```

进入时记下 \(D_0=\mathrm{pcmS}\)。退出用 **绝对水位 + 相对增量**，避免「deque 里已经很多还要再涨一截」或「decode 顶满涨不动」。

`pcmS` 只看 **主线程 PCM deque**，不含 FIFO。进入时 FIFO 往往还有几十毫秒，`hold` 把它们冻住，恢复时接着放，相当于白赚一段。

## 与暂停 / 后台的区别

| | 用户 `pause()` | `visHold` | 攒码流 |
| --- | --- | --- | --- |
| UI | 暂停 | 标签隐藏 | 仍是播放 |
| `clock.pause` | 是 | 是 | 是 |
| Worklet | `flushSpeaker` / `reset`，PCM 倒回 deque | 同左 | **`hold`，保留 FIFO** |
| 出帧 / 泵 PCM | 停 | 停 | 停 |
| 离开 | 用户 `play` | 标签可见 | PCM deque 够了 |

**禁止**走 `pause()` 做攒码流：`flushSpeaker` 会拆掉 FIFO，恢复更容易再点一声。`createClock.pause` 不是嵌套的；离开攒码流时若 `paused` 或 `visHold` 仍为真，不得 `clock.play()`。切条 `activate`、用户暂停、进后台都把 `rebuffering` 清掉。

无音频片、起播闸未过（尚未 `spkLive`）、FIFO 低但 **pcmS ≥ 80ms**（只是没泵）都不进攒码流。

## Worklet 契约

[`shorts-worklet.js`](../api/internal/httpapi/shorts-worklet.js)：

- `start`：`playing=true`，开始消费 FIFO。
- `hold`：`playing=false`，输出静音，**不** `reset`、不 `drain`。
- `reset`：只给用户暂停 / 切条 / 后台。会回 `drain`，主线程 `takeBackPcm`。
- `low`：FIFO 低于约 \(0.08\,\mathrm{s}\) 且尚未饿光，主线程可赶在补 0 之前 `enterRebuffer("low")`。
- `underrun`：本量子已补 0。此时刺啦可能已经发生；攒码流仍进入，避免连续补 0。

`maybeKickSpeaker` 在 `spkLive` 后直接 return。攒码流结束必须显式再 `start`，不能指望第一次 kick。

## 水位（实现常量）

| 常量 | 值 | 用途 |
| --- | --- | --- |
| `ARM_VIDEO` / `ARM_AUDIO` | 0.3s / 0.6s | 起播闸（与攒码流独立） |
| `PCM_AHEAD` | 0.4s | 播放中 Worklet FIFO 目标（曾 0.25s，过短会刚 leave 又 `low`） |
| Worklet `low` | ≈ 0.08s | 预告断流 |
| 进入：`pcmS < 0.08` | | 已解码 PCM 也快空了才冻；deque 里还有货只泵不冻 |
| `REBUFFER_HIGH` | 0.6s | 退出：PCM deque 绝对水位 |
| `REBUFFER_GAIN` | 0.5s | 退出：相对 \(D_0\) 至少又攒了这么多 |
| `REBUFFER_MIN_MS` | 180 | 最短冻结 |
| `LISTEN_BUDGET`（视频） | 0.5s | decode 听路视频 held |
| `LISTEN_AUDIO_BUDGET` | 0.85s | decode 听路音频 held，比视频多留一点 |

leave 日志打在 **泵 FIFO 之后**，此时 `fifo` 应接近 `PCM_AHEAD`，不要把「刚够 0.4s deque、FIFO 还是 hold 残留的 80ms」当成离开瞬间的扬声器水位。

## 为何还要改 demux / decode

攒码流只解决「空了别刺啦」。若恢复后 demux 仍先拉视频，音频会再次先空：leave 时常见 `vS≈0.9`、`pcmS≈0.4`；下一次 enter 时 `vS` 仍有 0.7s 而 `pcmS=0`。

根因：demux 循环曾经

```text
await demuxOne(video);  // iter.next() 可能堵在拉分片
await demuxOne(audio);  // 轮不到
```

听路且 `aCredit > 0` 时改为 **先音频后视频**。邻路 / 无音频 credit 仍先视频（封面 GOP）。

decode 侧 `submitBudget` 本就会在音频缺口更大时先送 AAC；听路音频 frame 预算改为 `LISTEN_AUDIO_BUDGET`，避免视频 held 顶满后音频仍只有半秒。

packet pipe 上限、邻路 0.25s、邻路视频轴超过约 2s 停 demux，不变。

## 主线程呈现

`tick`：`rebuffering` 时只 `maybeLeaveRebuffer` + `paintFirst`（已 primed 则不再重画，停在当前帧），**不** `takeVideoOne`。`reportHeld` 仍按 50ms 上报，decode 才能按 held 背压。

`mediaTime()` 优先 `shownPts`，冻结期间进度条停在最后一帧 PTS。

## 日志

诊断期去掉 200ms `sync`、`tick-stat`、Worklet 周期 stat、decoder/worker 流水。相关打印：

| 标签 | 何时 |
| --- | --- |
| `pcm-low` | FIFO 低水位（约 800ms 节流） |
| `pcm-underrun` | Worklet / ScriptProcessor 已补 0；攒码流中不再刷 |
| `pcm-underrun pktq` | underrun 后立刻 `dumpq`，AAC 包队列是当场数 |
| `pcm-buffer enter` / `leave` | 状态迁移 |

字段：`fifo`、`pcmS`/`pcmN`、`aacS`/`aacN`、`adq`、`vS`/`vN`、`pktAge`、`tick`/`tickMax`、`spk`、`armed`、`paused`、`buf`、`d0`。

页面不再每 200ms 调 `logHot()`（仍保留函数，以免误以为引擎没同步状态）。`window-cost` 在 `shorts.html`，与攒码流无关。

## 明确不做

- 用 `AudioContext.suspend()` 或只把 `gain` 打成 0。
- 给 clock 再加一套独立接口（`pause`/`play` 已冻结 `held`）。
- 片尾 / PTS 空洞当成缺网（`pumpSpeaker` 里 FIFO 近空且下包过未来仍挡住；EOS 不靠攒码流死等）。
- 补偿 DAC 相对墙钟的长期漂移（见 av-sync「明确不做」）。

## 相关代码

| 文件 | 职责 |
| --- | --- |
| `shorts-engine.js` | `enterRebuffer` / `leaveRebuffer` / `maybeLeaveRebuffer`、水位、冻钟、泵 FIFO |
| `shorts-worklet.js` | `hold` / `start` / `low` / `underrun` |
| `shorts-worker-src.js` 与打包后的 `shorts-worker.js` | 听路先 demux 音频 |
| `shorts-decode-worker.js` | `dumpq`、听路音频 0.85s 预算 |
| `shorts.html` | 不再 200ms `logHot` |
| `shorts_test.go` | 字符串契约：`pcm-buffer`、`hold`、`PCM_AHEAD = 0.4`、`listen && aCredit > 0` |
