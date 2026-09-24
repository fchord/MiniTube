# 短视频 AV 同步

本文记录 `/shorts` webcodecs 引擎里音视频相对墙钟的对齐策略：单钟、音频基准、TryToMatch / LostMatch。实现以 [`api/internal/httpapi/shorts-engine.js`](../api/internal/httpapi/shorts-engine.js) 为准。队列、硬解窗口与起播闸见 [`shorts-playback.md`](shorts-playback.md)。PCM 不足时冻钟再播见 [`shorts-rebuffer.md`](shorts-rebuffer.md)。

引擎已按下列设计实现。PCM 攒码流与 FIFO 深度随 [`shorts-rebuffer.md`](shorts-rebuffer.md) 迭代。

## 改之前

曾经两路墙钟 `vclock` / `aclock`，起播 `min(首视频pts, 首音频pts)` 同时开机，之后无校正环：视频 `takeDueOne` + `drop-late`，音频只填 FIFO。无音频时视频自己 `set` 钟。下文是现行契约。

墙钟（秒）：

\[
\mathrm{clock.get()} = \texttt{performance.now()}/1000 - \mathrm{diff}.
\]

`createClock.set(pts)` 令 \(\mathrm{diff} = t_{\mathrm{wall}} - \mathrm{pts}\)。pause 把当前媒体时间冻进 `held`；play 再接上。起播闸仍是听路视频约 \(0.3\,\mathrm{s}\)、音频约 \(0.6\,\mathrm{s}\)、FIFO 约 \(400\,\mathrm{ms}\)；封面停在第一帧。loop 用绝对 PTS 接龙，不重设 \(\mathrm{clock}\)。FIFO 将空时同一套 `pause`/`play` 用于攒码流，不走用户暂停的 `flushSpeaker`。

## 原则

1. **只有一个** \(\mathrm{clock}\)。
2. **以音频为基准。** 只有音频 `set` \(\mathrm{clock}\)。视频始终跟 \(\mathrm{clock}\) 比，从不跟 \(a_{\mathrm{pts}}\) 直接比，也从不 `set` 钟。
3. 音频在 \(\mathrm{clock}\) **之后**还有 FIFO，比对和跳钟都要扣掉这段延迟，用放音点而不是入队前沿。
4. 起播分类与播放中视频同一套：落后 \(3\,\mathrm{s}\) 内、超前 \(1\,\mathrm{s}\) 内 TryToMatch。
5. loop 不单开规则，走音频跳钟 + 视频 LostMatch。
6. 用户暂停和切条都 `flushSpeaker`：立刻停 PCM，FIFO 倒回该路 deque。暂停后再 play 从本路预填；切条由下一条填 FIFO。
7. 任何没被上面分支覆盖、无法同步的情况，**不准把画面卡住**；按帧间隔保底往外推。

## 状态与阈值

\[
T_{\mathrm{tight}} = 0.2\,\mathrm{s},\qquad
T_{\mathrm{lost}}^{\mathrm{ahead}} = 1.0\,\mathrm{s},\qquad
T_{\mathrm{catch}}^{\mathrm{behind}} = 3.0\,\mathrm{s}.
\]

\(T_{\mathrm{tight}}\)：视频已对齐带；音频放音点过晚也用这条。音频过早跳钟仍是 \(+1\,\mathrm{s}\)。视频落后在 \(3\,\mathrm{s}\) 内仍 TryToMatch（加速）；超前超过 \(1\,\mathrm{s}\) 才 LostMatch。解码打顿约 1s 时用 2 倍速追，不锁死偏移。

LostMatch 滞回：\(v_{\mathrm{pts}} < \mathrm{clock}-3\) 或 \(v_{\mathrm{pts}} > \mathrm{clock}+1\) 进入；回到 TryToMatch 要 \(v_{\mathrm{pts}} \ge \mathrm{clock}-2.8\) 且 \(v_{\mathrm{pts}} \le \mathrm{clock}+0.8\)。

视频相对 \(\mathrm{clock}\)：

| 状态 | 条件 | 含义 |
| --- | --- | --- |
| TryToMatch | \(\mathrm{clock}-3 \le v_{\mathrm{pts}} \le \mathrm{clock}+1\)（滞回后） | 按时上屏或变帧率去对齐 |
| LostMatch | \(v_{\mathrm{pts}} < \mathrm{clock}-3\) 或 \(v_{\mathrm{pts}} > \mathrm{clock}+1\) | 不再追，平行保持 \(v_{\mathrm{diff}}\) |

\[
\Delta = v_{\mathrm{pts}} - \mathrm{clock}.
\]

TryToMatch 用这个 \(\Delta\)，**不用** \(v_{\mathrm{diff}}\)。LostMatch 才用 \(v_{\mathrm{pts}} - v_{\mathrm{diff}}\) 去和 \(\mathrm{clock}\) 比。

帧间隔 \(\mathrm{dur}\) 夹在 \([1/120,\,0.1]\) 秒：优先 \(v_{\mathrm{pts}}^{\mathrm{cur}} - v_{\mathrm{pts}}^{\mathrm{last}}\)；不在区间则用解码 `duration`；再否则 \(1/30\)（默认 fps）。加速仅当 video deque 够深——`dequeSeconds(st.video) \ge 0.2` 或 `length \ge 3`——否则 \(\mathrm{rate}=1\)。

每 tick 视频最多决定队首一帧是否出队。加速态不要在同一 rAF 里把 due 帧全丢掉。

## FIFO 延迟与放音点

PCM 先入 Worklet FIFO 再出扬声器。健康时队列约 \(q_s \approx 0.4\,\mathrm{s}\)，**入队前沿** \(a_{\mathrm{enq}}\)（FIFO 末包 \(\mathrm{pts}+\mathrm{duration}\)）比正在响的声音超前 \(q_s\)。\(\mathrm{clock}\) 表示「现在该响哪一刻」，必须和 **放音点** 比，不能和 \(a_{\mathrm{enq}}\) 比。

\[
a_{\mathrm{play}} =
\begin{cases}
a_{\mathrm{enq}} - q_s & q_s > 0, \\
\text{下一包 } a_{\mathrm{pts}} & \text{否则.}
\end{cases}
\qquad q_s = \texttt{pcmQueuedSec()}.
\]

等价写法：把入队包去比 \(\mathrm{clock}+q_s\)。健康预填时 \(a_{\mathrm{play}}\approx\mathrm{clock}\)、\(a_{\mathrm{enq}}\approx\mathrm{clock}+q_s\)。若拿队尾直接去比 \(\mathrm{clock}+T_{\mathrm{lost}}\)，几乎永不跳钟，或把正常的 \(400\,\mathrm{ms}\) 超前当成失步。

跳钟、起播 `set` 都用 \(a_{\mathrm{play}}\)（起播尚未 `start` 时，FIFO 已填满则 \(a_{\mathrm{play}}\) 就是队首包 PTS，不是末包）。

蓝牙 / DAC 硬件延迟另算，本文不做。

## 起播

保留 \(0.3\,\mathrm{s}\) / \(0.6\,\mathrm{s}\) / \(400\,\mathrm{ms}\) 预填。「能拿到首 PTS」是开钟的必要条件，不是充分条件。封面仍停住。无音频：不 `set` \(\mathrm{clock}\)（没有音频基准），视频走「保底出帧」。

有音频时：FIFO 预满后

\[
\mathrm{clock.set}(a_{\mathrm{play}}).
\]

**只由音频开机**，不用 \(\min(v,a)\)，也不用 \(\lvert v_{\mathrm{pts}}-a_{\mathrm{pts}}\rvert\) 分类。然后视频用同一时刻的队首 \(v_{\mathrm{pts}}\) 看 \(\lvert\Delta\rvert=\lvert v_{\mathrm{pts}}-\mathrm{clock}\rvert\)：

- \(\mathrm{clock}-3 \le v_{\mathrm{pts}} \le \mathrm{clock}+1\)：\(v_{\mathrm{diff}}=0\)，TryToMatch。
- 否则：\(v_{\mathrm{diff}} = v_{\mathrm{pts}} - \mathrm{clock}\)，LostMatch；立刻上屏一次队首，避免卡在等号上。

（开钟瞬间 \(\mathrm{clock}=a_{\mathrm{play}}\approx a_{\mathrm{pts}}^{\mathrm{head}}\)，故 \(\lvert v-\mathrm{clock}\rvert\) 与 \(\lvert v-a\rvert\) 数值碰巧相同，但之后视频只认 \(\mathrm{clock}\)。旧稿 \(\min(v,a)\) 会在「视频更早」时把钟绑到视频上，违背音频基准。）

## 播放中的 audio

每个 `pumpSpeaker` 周期看 \(a_{\mathrm{play}}\)（已扣 FIFO）：

- 若 \(a_{\mathrm{play}} < \mathrm{clock} - T_{\mathrm{tight}}\) **或** \(a_{\mathrm{play}} > \mathrm{clock} + T_{\mathrm{lost}}\)：`clock.set(a_{\mathrm{play}})`，清 `v_last_clock` 与加速标记。这是播放中 **唯一** 改钟的路径。
- 否则继续把 FIFO 填到 \(400\,\mathrm{ms}\)，不按包 due。下一包相对入队前沿过未来（现状 `pts > now+0.02` 且 FIFO 近空）仍挡住，比较时同样用 \(\mathrm{clock}+q_s\) 而不是裸 \(\mathrm{clock}\)。

不对称是故意的：欠载要把钟拉回正在响的声音；FIFO 允许入队略超前，只有放音点跳变（\(>1\,\mathrm{s}\)）才跟。

## 播放中的 video

\(v_{\mathrm{pts}}\) 是队首帧。\(\Delta = v_{\mathrm{pts}} - \mathrm{clock}\)。视频只读 \(\mathrm{clock}\)，不读 \(a_{\mathrm{pts}}\)。

### TryToMatch：已对齐（\(\lvert\Delta\rvert \le T_{\mathrm{tight}}\)）

\(v_{\mathrm{pts}} < \mathrm{clock}\) 则推出。同 tick 可收到最新 due（此带最多几帧）。清 `v_acceleration`，`v_last_clock = 0`。

### TryToMatch：可追上（落后 \(\le 3\,\mathrm{s}\) 或超前 \(\le 1\,\mathrm{s}\)）

**落后** \(v_{\mathrm{pts}} < \mathrm{clock}\)：加速，`v_acceleration = true`。

- `v_last_clock == 0`：立刻推出，记 `v_last_clock = clock`。
- 否则：\(\mathrm{rate} =\)（管道够深则 \(2\)，否则 \(1\)）。当 \(\mathrm{clock} - v_{\mathrm{last\_clock}} > \mathrm{dur}/\mathrm{rate}\) 时推出，并更新 `v_last_clock`。

**超前** \(v_{\mathrm{pts}} > \mathrm{clock}\)：减速，用 \(2\cdot\mathrm{dur}\) 的节拍推出，**允许在 \(v_{\mathrm{pts}}\) 尚未到 \(\mathrm{clock}\) 时出帧**。每出一帧 \(\mathrm{clock}\) 多走约一倍 \(\mathrm{dur}\)，\(\Delta\) 向 \(0\) 收。

### LostMatch（落后 \(>3\,\mathrm{s}\) 或超前 \(>1\,\mathrm{s}\)）

不再 `drop-late`。平行时间轴：

- 若 \(\lvert (v_{\mathrm{pts}} - v_{\mathrm{diff}}) - \mathrm{clock}\rvert < T_{\mathrm{tight}}\)：同一段失同步。若 \(v_{\mathrm{pts}} - v_{\mathrm{diff}} < \mathrm{clock}\) 则推出。
- 否则：\(v_{\mathrm{diff}} = v_{\mathrm{pts}} - \mathrm{clock}\)，并推出当前帧。

经滞回回到 TryToMatch 时把 \(v_{\mathrm{diff}}\) 清零。

## loop

demux 把 PTS 接成绝对时间，引擎不在 loop 点重设 \(\mathrm{clock}\)。两条轨很难同一瞬间回头，但都落在上面的规则里：

- **音频先回头：** \(a_{\mathrm{play}}\) 从片尾掉到片头，\(a_{\mathrm{play}} < \mathrm{clock} - T_{\mathrm{tight}}\)，音频 `set` \(\mathrm{clock}\) 到新的放音点。视频还在片尾，\(\lvert\Delta\rvert\) 立刻大于 \(T_{\mathrm{lost}}\)，LostMatch（\(v_{\mathrm{diff}}\) 约等于片长）。视频按偏移继续画，直到视频也回头，\(\lvert\Delta\rvert\) 掉回带内，再 TryToMatch。
- **视频先回头：** \(v_{\mathrm{pts}}\) 掉到片头，\(\Delta\) 立刻巨大，LostMatch，\(v_{\mathrm{diff}} = v_{\mathrm{pts}}-\mathrm{clock}\)（约负片长）。音频仍按旧钟走；视频用偏移把片头当「平行轨」画。音频回头后跳钟，\(\lvert\Delta\rvert\) 收回，再 TryToMatch。

## 暂停与切条

两条路径同一套扬声器处理：`flushSpeaker`（Worklet `reset`、立刻静音）+ `pcm-restore` / `takeBackPcm`（未放完的 FIFO 倒回**当时那一路**的 audio deque）。不要只用 `gain=0`（FIFO 仍被吃掉），也不要用 `AudioContext.suspend()`。

| | 立刻静音 | FIFO → 该路 deque | 之后谁填 FIFO |
| --- | --- | --- | --- |
| 用户暂停 | 要 | 要 | 本路 `play` 再预填 |
| 切条 / `activate` | 要 | 要（倒回**滑走的那条**） | **下一条**起播时预填 |

暂停时 \(\mathrm{clock.pause()}\)，画面立刻停。play 时钟从 `held` 继续，从已倒回的 deque 再填 FIFO。切条时本路钟 `clear`，下一条走起播（音频 `set` \(\mathrm{clock}\)、预填 \(400\,\mathrm{ms}\)）。

切到别的 App / 标签隐藏：不能只 `clock.pause()` 而让 Worklet 继续吃 FIFO。隐藏时 `visHold` + `flushSpeaker`（立刻静音、PCM 倒回 deque），tick 不再泵音频、不再出帧。回来再 `clock.play()`、预填 FIFO；若画面仍落后，`visCatch` 一次追到钟附近，不要把约 1s 的落后锁进 LostMatch。空 FIFO 时不得用起播 `t0a` 把钟打回 0。

**攒码流**（网/demux 跟不上）：同一 `clock.pause()`，但 Worklet 发 `hold` 而不是 `reset`。细则见 [`shorts-rebuffer.md`](shorts-rebuffer.md)。不要用用户暂停路径去攒码。

## 保底出帧

上面分支有明确「等」的含义（已对齐且 \(v_{\mathrm{pts}}>\mathrm{clock}\) 等钟；减速等 \(2\cdot\mathrm{dur}\)）。除此之外，**任何无法同步的情况都不得把画面卡住**，包括：\(\mathrm{clock}\) 未 primed（无音频）、PTS 非有限、模式无法判定、LostMatch 等偏移但间隔已超过 \(\mathrm{dur}\)、漏网的 `else`。

保底间隔

\[
\mathrm{dur}_{\mathrm{safe}} = \mathrm{clamp}\bigl(\mathrm{dur},\, 1/120,\, 0.1\bigr)
\]

（\(\mathrm{dur}\) 仍按「状态与阈值」取值：delta pts / 解码 duration / \(1/30\)）。若距上一帧上屏已 \(\ge \mathrm{dur}_{\mathrm{safe}}\)，就推出队首。无音频时整条听路都走这套节拍，不把视频去 `set` \(\mathrm{clock}\)。

## 与现状对照

| 现状 | 设计 |
| --- | --- |
| `vclock` + `aclock` | 一个 \(\mathrm{clock}\) |
| 双钟 \(\min(v,a)\) 开机 | 只 `set(a_{\mathrm{play}})`；视频用 \(\lvert v-\mathrm{clock}\rvert\) 分类 |
| 视频 `drop-late` \(1\,\mathrm{s}\) | LostMatch 平行走 |
| 落后时一 rAF 追到最新 due | \(0.2\sim 3\,\mathrm{s}\) 落后最多 \(2\times\) 帧率 |
| 超前干等到 \(v_{\mathrm{pts}}\le\mathrm{clock}\) | \(0.2\sim 1\,\mathrm{s}\) 超前半速出帧 |
| 音频纯 FIFO，裸 pts 近空保护 | FIFO 保留；用 \(a_{\mathrm{play}}=a_{\mathrm{enq}}-q_s\) 跳钟 |
| loop 不重设墙钟 | 仍不重设；音频先/视频先回头走跳钟 + LostMatch |
| 暂停 / 切条：`flushSpeaker` + `pcm-restore` | 相同（切条倒回滑走那条，下一条自己填 FIFO） |
| 无音频走 `vclock.set(首帧)` | 无音频不 `set` 钟，保底出帧 |

切条 `clear` / 封面闸不变。PCM 预填深度见 [`shorts-rebuffer.md`](shorts-rebuffer.md) 的 `PCM_AHEAD`。

## 明确不做

- 补偿 AudioWorklet / DAC 相对墙钟的长期漂移。
- 补偿蓝牙或输出设备延迟。
- 把音频改成按 \(\mathrm{clock}\) 逐包 `due`。
- 用用户暂停的 `flushSpeaker` 去实现网卡顿。
