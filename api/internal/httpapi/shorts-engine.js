const TARGET_HOLD = 0.5;
const FIRST_FRAME_MS = 4000;
const SYNC_TIGHT = 0.2;
const SYNC_LOST = 1;
const SYNC_CATCH_BEHIND = 3;
const SYNC_BACK = 0.8;
const DUR_MIN = 1 / 120;
const DUR_MAX = 0.1;
const DUR_FALLBACK = 1 / 30;
const PIPE_ACCEL_SEC = 0.2;
const PIPE_ACCEL_N = 3;
const HOLE_THRESH = 0.2;
const HELD_EVERY_MS = 50;
const PCM_AHEAD = 0.4;
const ARM_VIDEO = 0.3;
const ARM_AUDIO = 0.6;
const NEIGHBOR_AUDIO = 0.2;
const REBUFFER_HIGH = 0.6;
const REBUFFER_GAIN = 0.5;
const REBUFFER_MIN_MS = 180;

function videoIsLost(pts, now) {
  return pts < now - SYNC_CATCH_BEHIND || pts > now + SYNC_LOST;
}

function videoLeaveLost(pts, now) {
  return pts >= now - (SYNC_CATCH_BEHIND - 0.2) && pts <= now + SYNC_BACK;
}

function dequeRunSeconds(q) {
  if (!q || !q.length) return 0;
  let end = Number(q[0].pts) + (q[0].duration || 0);
  for (let i = 1; i < q.length; i++) {
    const f = q[i];
    const pts = Number(f.pts);
    if (!Number.isFinite(pts) || pts > end + HOLE_THRESH) break;
    end = Math.max(end, pts + (f.duration || 0));
  }
  return end - Number(q[0].pts);
}

function dequeSeconds(q) {
  let s = 0;
  for (let i = 0; i < q.length; i++) s += q[i].duration || 0;
  return s;
}

function pcmItemSec(x) {
  if (!x) return 0;
  const rate = x.sampleRate || 44100;
  if (x.samples && x.samples.length) return (x.samples.length >> 1) / rate;
  return Number(x.duration) || 0;
}

function skipPcmItem(x, skipSec) {
  if (!x || !x.samples || skipSec <= 1e-5) return x;
  const rate = x.sampleRate || 44100;
  const skipFrames = Math.min(x.samples.length >> 1, Math.max(0, Math.round(skipSec * rate)));
  const off = skipFrames * 2;
  if (off >= x.samples.length) return null;
  const rest = x.samples.slice(off);
  const restSec = (rest.length >> 1) / rate;
  return {
    pts: Number(x.pts) + skipFrames / rate,
    duration: restSec,
    sampleRate: rate,
    samples: rest
  };
}

function wallSec() {
  return performance.now() / 1000;
}

function createClock() {
  let diff = 0;
  let primed = false;
  let held = null;
  return {
    set: function (pts) {
      diff = wallSec() - Number(pts);
      primed = true;
      held = null;
    },
    syncFrom: function (other) {
      if (!other || !other.primed()) return false;
      diff = other.diffOf();
      primed = true;
      held = other.heldOf();
      return true;
    },
    diffOf: function () { return diff; },
    heldOf: function () { return held; },
    get: function () {
      if (held != null) return held;
      return wallSec() - diff;
    },
    primed: function () { return primed; },
    pause: function () {
      if (!primed || held != null) return;
      held = wallSec() - diff;
    },
    play: function () {
      if (held == null) return;
      diff = wallSec() - held;
      held = null;
    },
    clear: function () {
      primed = false;
      held = null;
    }
  };
}

function resampleStereo(src, srcRate, dstRate, rs) {
  if (!src || src.length < 2) return src;
  if (Math.abs(srcRate - dstRate) < 1) return src;
  const srcFrames = src.length >> 1;
  const step = srcRate / dstRate;
  let pos = rs && Number.isFinite(rs.pos) ? rs.pos : 0;
  const out = new Float32Array(Math.max(2, Math.ceil(srcFrames / step + 4) * 2));
  let n = 0;
  function at(p) {
    if (p < 0 && rs && rs.hasPrev) {
      const t = p + 1;
      return [
        rs.prevL + (src[0] - rs.prevL) * t,
        rs.prevR + (src[1] - rs.prevR) * t
      ];
    }
    if (p < 0) p = 0;
    const i0 = Math.min(srcFrames - 1, Math.floor(p));
    const i1 = Math.min(srcFrames - 1, i0 + 1);
    const t = p - i0;
    return [
      src[i0 * 2] + (src[i1 * 2] - src[i0 * 2]) * t,
      src[i0 * 2 + 1] + (src[i1 * 2 + 1] - src[i0 * 2 + 1]) * t
    ];
  }
  while (Math.floor(pos) + 1 < srcFrames || (pos < 0 && srcFrames > 0)) {
    if (pos >= srcFrames) break;
    if (pos >= 0 && Math.floor(pos) + 1 >= srcFrames) break;
    const lr = at(pos);
    out[n * 2] = lr[0];
    out[n * 2 + 1] = lr[1];
    n += 1;
    pos += step;
  }
  if (rs) {
    rs.pos = pos - srcFrames;
    rs.prevL = src[(srcFrames - 1) * 2];
    rs.prevR = src[(srcFrames - 1) * 2 + 1];
    rs.hasPrev = true;
  }
  return n * 2 === out.length ? out : out.subarray(0, n * 2);
}

export async function create(opts) {
  opts = opts || {};
  const workerVer = (opts.assetVersion || (typeof import.meta !== "undefined" && import.meta.url && new URL(import.meta.url).searchParams.get("v")) || "");
  const workers = new Map();
  const pipes = new Map();
  const resumeAt = Object.create(null);
  const shownPts = Object.create(null);
  const audio = { ctx: null, node: null, sp: null, gain: null, muted: true, blocked: false, gen: 0, q: [], rs: { pos: 0, hasPrev: false }, spkLive: false, workletQs: 0 };
  let activeId = null;
  let warmId = null;
  let warmIds = [];
  let prevId = null;
  let heldAt = 0;
  let paused = true;
  let visHold = false;
  let startArmed = false;
  let rebuffering = false;
  let rebufferD0 = 0;
  let rebufferAt = 0;
  const clock = createClock();
  const sync = { lost: false, vDiff: 0, lastClock: null, lastPresentWall: 0, visCatch: false };
  let raf = 0;
  const onScreen = Object.create(null);
  const primed = Object.create(null);
  const parked = Object.create(null);
  const idleBmp = Object.create(null);
  const idleGen = Object.create(null);
  const seqOf = Object.create(null);
  const renderedCount = Object.create(null);
  const lastRenderedPts = Object.create(null);
  let onMuteChange = opts.onMuteChange || function () {};
  let onError = opts.onError || function () {};
  let onUi = opts.onUi || function () {};
  let onLog = opts.onLog || function () {};

  function slog() {
    try { onLog.apply(null, [].slice.call(arguments)); } catch (e) {}
  }

  let lastTickMs = 0;
  let tickMax1s = 0;
  let tickWinAt = 0;
  let starveLogAt = 0;
  let lowLogAt = 0;

  function starveFields(extra) {
    const st = pipes.get(activeId);
    const pkt = (st && st.pkt) || {};
    const pktAge = st && st.pktAt ? (performance.now() - st.pktAt) : -1;
    const parts = [
      "seq=" + seqLabel(activeId),
      extra || "",
      "fifo=" + pcmQueuedSec().toFixed(3),
      "pcmS=" + dequeSeconds(st && st.audio).toFixed(3),
      "pcmN=" + ((st && st.audio && st.audio.length) || 0),
      "aacS=" + Number(pkt.aps || 0).toFixed(3),
      "aacN=" + (pkt.apd || 0),
      "adq=" + (pkt.adq || 0),
      "vS=" + dequeSeconds(st && st.video).toFixed(3),
      "vN=" + ((st && st.video && st.video.length) || 0),
      "pktAge=" + (pktAge >= 0 ? String(Math.round(pktAge)) : "-"),
      "tick=" + lastTickMs.toFixed(1),
      "tickMax=" + tickMax1s.toFixed(1),
      "spk=" + (audio.spkLive ? 1 : 0),
      "armed=" + (startArmed ? 1 : 0),
      "paused=" + (paused ? 1 : 0),
      "buf=" + (rebuffering ? 1 : 0),
      "ctx=" + (audio.ctx ? audio.ctx.state : "none")
    ];
    return parts.filter(function (x) { return x !== ""; }).join(" ");
  }

  function requestDumpq() {
    const st = pipes.get(activeId);
    const pair = workers.get(activeId);
    if (!st || !pair) return;
    st.wantPktDump = true;
    try { pair.decode.postMessage({ type: "dumpq", id: activeId }); } catch (e) {}
  }

  function logStarve(tag, extra) {
    if (rebuffering && tag !== "pktq") return;
    const t = performance.now();
    if (tag !== "pktq" && starveLogAt && t - starveLogAt < 200) return;
    if (tag !== "pktq") starveLogAt = t;
    slog("pcm-underrun", tag, starveFields(extra));
    if (tag !== "pktq") requestDumpq();
  }

  function holdWorklet() {
    if (audio.usePort && audio.node && audio.node.port) {
      try { audio.node.port.postMessage({ type: "hold", gen: audio.gen }); } catch (e) {}
    }
  }

  function enterRebuffer(st, why) {
    if (rebuffering || paused || visHold || !startArmed || !audio.spkLive) return;
    if (!st || st.noAudio) return;
    const pcmS = dequeSeconds(st.audio);
    if (pcmS >= 0.08) return;
    rebuffering = true;
    rebufferD0 = pcmS;
    rebufferAt = performance.now();
    clock.pause();
    holdWorklet();
    slog("pcm-buffer", "enter", why, starveFields("d0=" + rebufferD0.toFixed(3)));
  }

  function leaveRebuffer(st, why) {
    if (!rebuffering) return;
    rebuffering = false;
    if (!paused && !visHold) clock.play();
    if (!paused && !visHold && st) {
      pumpSpeaker(st);
      if (audio.usePort && audio.node && audio.node.port) {
        try { audio.node.port.postMessage({ type: "start", gen: audio.gen }); } catch (e) {}
        audio.spkLive = true;
      }
    }
    slog("pcm-buffer", "leave", why, starveFields("d0=" + rebufferD0.toFixed(3)));
  }

  function maybeLeaveRebuffer(st) {
    if (!rebuffering || !st) return;
    if (paused || visHold) {
      rebuffering = false;
      return;
    }
    if (performance.now() - rebufferAt < REBUFFER_MIN_MS) return;
    const pcmS = dequeSeconds(st.audio);
    if (pcmS - rebufferD0 >= REBUFFER_GAIN || pcmS >= REBUFFER_HIGH) leaveRebuffer(st, "full");
  }

  function seqLabel(id) {
    const n = seqOf[id];
    return Number.isFinite(n) ? n : -1;
  }

  function roleOf(id) {
    if (id && id === activeId) return "listen";
    if (id && id === warmId) return "warm";
    if (id && warmIds.indexOf(id) >= 0) return "warm";
    if (id && id === prevId) return "prev";
    return "idle";
  }

  function markRendered(id, pts) {
    if (!id || pts == null) return;
    if (lastRenderedPts[id] === pts) return;
    lastRenderedPts[id] = pts;
    renderedCount[id] = (renderedCount[id] || 0) + 1;
  }

  function logHot() {
    const st = pipes.get(activeId);
    const c = clock.primed() ? clock.get() : NaN;
    const ap = audioPlayPts(st);
    const vp = st && st.video.length ? Number(st.video[0].pts) : NaN;
    const shown = Number(lastRenderedPts[activeId]);
    const qs = pcmQueuedSec();
    let mode = "idle";
    if (!activeId) mode = "idle";
    else if (visHold) mode = "bg";
    else if (rebuffering) mode = "buf";
    else if (paused) mode = "pause";
    else if (!clock.primed()) mode = (st && st.noAudio) ? "dur" : (startArmed ? "fill" : "arm");
    else if (sync.lost) mode = "lost";
    else if (Number.isFinite(vp) && Number.isFinite(c)) {
      if (Math.abs(vp - c) <= SYNC_TIGHT) mode = "tight";
      else if (videoIsLost(vp, c)) mode = "lost";
      else mode = vp < c ? "accel" : "decel";
    } else mode = "play";
    const dv = Number.isFinite(vp) && Number.isFinite(c) ? vp - c : NaN;
    const da = Number.isFinite(ap) && Number.isFinite(c) ? ap - c : NaN;
    const line = [
      "seq=" + seqLabel(activeId),
      "mode=" + mode,
      "c=" + (Number.isFinite(c) ? c.toFixed(3) : "-"),
      "ap=" + (Number.isFinite(ap) ? ap.toFixed(3) : "-"),
      "vp=" + (Number.isFinite(vp) ? vp.toFixed(3) : "-"),
      "shown=" + (Number.isFinite(shown) ? shown.toFixed(3) : "-"),
      "dv=" + (Number.isFinite(dv) ? dv.toFixed(3) : "-"),
      "da=" + (Number.isFinite(da) ? da.toFixed(3) : "-"),
      "vdiff=" + (sync.lost ? Number(sync.vDiff).toFixed(3) : "0"),
      "qs=" + qs.toFixed(3)
    ].join(" ");
    if (line === logHot.last) return;
    logHot.last = line;
    slog("sync", line);
  }

  function unlockAudio() {
    if (!audio.ctx) {
      const ctx = new AudioContext();
      audio.ctx = ctx;
      audio.gain = ctx.createGain();
      audio.gain.gain.value = audio.muted ? 0 : 1;
      audio.gain.connect(ctx.destination);
      const sp = ctx.createScriptProcessor(2048, 2, 2);
      sp.onaudioprocess = onScriptProc;
      const keep = ctx.createGain();
      keep.gain.value = 0;
      let driver;
      if (typeof ctx.createConstantSource === "function") {
        driver = ctx.createConstantSource();
        driver.offset.value = 0;
      } else {
        driver = ctx.createOscillator();
        driver.frequency.value = 20;
      }
      driver.connect(keep);
      keep.connect(sp);
      driver.start();
      sp.connect(audio.gain);
      audio.sp = sp;
      audio.driver = driver;
      audio.node = sp;
      audio.usePort = false;
      if (!audio.q) audio.q = [];
      attachWorklet();
    }
    if (audio.ctx.state === "suspended") {
      const p = audio.ctx.resume();
      if (p && p.then) {
        p.then(function () {
          audio.blocked = audio.ctx.state !== "running";
          if (audio.gain && !audio.muted) audio.gain.gain.value = 1;
          syncMuteUi();
        }).catch(function () {});
      }
    }
    audio.blocked = audio.ctx.state !== "running";
    if (audio.gain && !audio.muted) audio.gain.gain.value = 1;
    syncMuteUi();
  }

  function attachWorklet() {
    if (audio.workletTried || !audio.ctx || !audio.ctx.audioWorklet) return;
    audio.workletTried = true;
    audio.ctx.audioWorklet.addModule("/static/shorts-worklet.js?v=" + workerVer).then(function () {
      if (!audio.ctx || audio.usePort) return;
      let node;
      try {
        node = new AudioWorkletNode(audio.ctx, "shorts-pcm", { numberOfInputs: 0, numberOfOutputs: 1, outputChannelCount: [2] });
      } catch (e) {
        node = new AudioWorkletNode(audio.ctx, "shorts-pcm", { numberOfOutputs: 1, outputChannelCount: [2] });
      }
      node.port.onmessage = onWorkletMsg;
      try { node.port.start(); } catch (e) {}
      node.connect(audio.gain);
      const leftover = audio.q || [];
      audio.q = [];
      if (audio.sp) {
        try { audio.sp.onaudioprocess = null; } catch (e) {}
        try { audio.sp.disconnect(); } catch (e) {}
      }
      if (audio.driver) {
        try { audio.driver.stop(); } catch (e) {}
        try { audio.driver.disconnect(); } catch (e) {}
      }
      audio.node = node;
      audio.usePort = true;
      audio.sentSec = 0;
      audio.dacAt = null;
      audio.workletQs = 0;
      node.port.postMessage({ type: "reset", gen: audio.gen });
      leftover.forEach(function (cur) {
        if (!cur || !cur.samples) return;
        enqueuePcm(cur.off ? cur.samples.slice(cur.off) : cur.samples);
      });
      slog("pcm-worklet", "rate=" + audio.ctx.sampleRate, "gen=" + audio.gen, "left=" + leftover.length);
      if (audio.gain && !audio.muted) audio.gain.gain.value = 1;
      if (activeId && startArmed && !paused) {
        pumpSpeaker(pipes.get(activeId));
        maybeKickSpeaker(pipes.get(activeId));
      }
    }).catch(function (e) {
      slog("pcm-worklet", "fail", (e && e.message) || e);
    });
  }

  function onWorkletMsg(ev) {
    const d = (ev && ev.data) || {};
    if (d.type === "stat") {
      if ((d.got || 0) > 0 || audio.spkLive) audio.workletQs = Number(d.qs) || 0;
      return;
    }
    if (d.type === "underrun") {
      audio.workletQs = Number(d.qs) || 0;
      logStarve("worklet", "cb=" + (d.cb || 0) + " n=" + (d.n || 0) + " got=" + (d.got || 0) + " nq=" + (d.nq || 0));
      const st = pipes.get(activeId);
      enterRebuffer(st, "underrun");
      if (activeId && startArmed && !paused && !rebuffering) pumpSpeaker(st);
      return;
    }
    if (d.type === "low") {
      audio.workletQs = Number(d.qs) || 0;
      const t = performance.now();
      if (!lowLogAt || t - lowLogAt > 800) {
        lowLogAt = t;
        slog("pcm-low", starveFields("qs=" + Number(d.qs || 0).toFixed(3)));
      }
      const st = pipes.get(activeId);
      enterRebuffer(st, "low");
      if (activeId && startArmed && !paused && !rebuffering) pumpSpeaker(st);
      return;
    }
    if (d.type === "drain") {
      const rec = audio.drains && audio.drains[d.gen];
      if (!rec) return;
      delete audio.drains[d.gen];
      takeBackPcm(rec.owner, rec.inflight, Number(d.qs));
    }
  }

  function pcmQueuedSec() {
    if (audio.usePort) return audio.workletQs || 0;
    const q = audio.q || [];
    const rate = (audio.ctx && audio.ctx.sampleRate) || 48000;
    let frames = 0;
    for (let i = 0; i < q.length; i++) {
      const cur = q[i];
      if (!cur || !cur.samples) continue;
      frames += (cur.samples.length - (cur.off || 0)) >> 1;
    }
    return frames / rate;
  }

  function copyPcm(samples) {
    const out = new Float32Array(samples.length);
    out.set(samples);
    return out;
  }

  function enqueuePcm(samples) {
    if (!samples || !samples.length) return;
    const rate = (audio.ctx && audio.ctx.sampleRate) || 48000;
    if (audio.usePort && audio.node && audio.node.port) {
      let copy;
      try { copy = copyPcm(samples); } catch (e) { return; }
      audio.node.port.postMessage({ type: "pcm", samples: copy, gen: audio.gen });
      audio.workletQs = (audio.workletQs || 0) + ((copy.length >> 1) / rate);
      return;
    }
    audio.q = audio.q || [];
    audio.q.push({ samples: samples, off: 0 });
  }

  function resetVideoPace() {
    sync.lastClock = null;
  }

  function resetSync() {
    sync.lost = false;
    sync.vDiff = 0;
    sync.lastClock = null;
    sync.lastPresentWall = 0;
    sync.visCatch = false;
  }

  function noteEnqEnd(item) {
    if (!item) return;
    const pts = Number(item.pts);
    const dur = Number(item.duration);
    const end = pts + (Number.isFinite(dur) && dur > 0 ? dur : pcmItemSec(item));
    if (!Number.isFinite(end)) return;
    if (!Number.isFinite(audio.enqEnd) || end > audio.enqEnd) audio.enqEnd = end;
  }

  function audioPlayPts(st) {
    const qs = pcmQueuedSec();
    if (qs > 1e-4 && Number.isFinite(audio.enqEnd)) {
      audio.lastPlay = audio.enqEnd - qs;
      return audio.lastPlay;
    }
    if (st && st.audio.length && Number.isFinite(st.audio[0].pts)) {
      audio.lastPlay = Number(st.audio[0].pts);
      return audio.lastPlay;
    }
    if (Number.isFinite(audio.lastPlay)) return audio.lastPlay;
    if (Number.isFinite(audio.enqEnd)) return audio.enqEnd;
    if (!clock.primed() && Number.isFinite(audio.t0a)) return Number(audio.t0a);
    return NaN;
  }

  function maybeSnapClock(st) {
    if (!st || st.noAudio || visHold || !clock.primed() || !audio.spkLive) return;
    const ap = audioPlayPts(st);
    if (!Number.isFinite(ap)) return;
    const now = clock.get();
    if (ap < now - SYNC_TIGHT || ap > now + SYNC_LOST) {
      clock.set(ap);
      resetVideoPace();
    }
  }

  function startClockFromAudio(st) {
    if (clock.primed() || !st || st.noAudio) return;
    const ap = audioPlayPts(st);
    if (!Number.isFinite(ap)) return;
    clock.set(ap);
    resetVideoPace();
    const now = clock.get();
    const vp = st.video.length ? Number(st.video[0].pts) : NaN;
    sync.lost = false;
    sync.vDiff = 0;
    if (Number.isFinite(vp) && videoIsLost(vp, now)) {
      sync.lost = true;
      sync.vDiff = vp - now;
      slog("lost-match", "start", "v=" + vp.toFixed(3), "c=" + now.toFixed(3), "diff=" + sync.vDiff.toFixed(3));
    }
    slog(
      "clock-start",
      "a=" + ap.toFixed(3),
      Number.isFinite(vp) ? "v=" + vp.toFixed(3) : "v=-",
      "lost=" + (sync.lost ? 1 : 0),
      "qs=" + pcmQueuedSec().toFixed(3),
      st.video.length ? "vh=" + Number(st.video[0].pts).toFixed(3) + ".." + Number(st.video[st.video.length - 1].pts).toFixed(3) : "vh=-"
    );
  }

  function pumpSpeaker(st) {
    if (!st || paused || visHold || rebuffering || !startArmed) return 0;
    maybeSnapClock(st);
    const now = clock.primed() ? clock.get() : -Infinity;
    let n = 0;
    while (st.audio.length && pcmQueuedSec() < PCM_AHEAD) {
      const nxt = st.audio[0];
      const qs = pcmQueuedSec();
      if (clock.primed() && qs < 1e-4 && Number.isFinite(nxt.pts) && nxt.pts > now + qs + 0.02) break;
      const a = shiftA(st);
      audio.inflight = audio.inflight || [];
      audio.inflightId = activeId;
      audio.inflight.push(a);
      noteEnqEnd(a);
      const src = a.samples;
      const rate = a.sampleRate;
      let samples;
      if (audio.ctx && Math.abs(rate - audio.ctx.sampleRate) >= 1) {
        samples = resampleStereo(src, rate, audio.ctx.sampleRate, audio.rs);
      } else {
        if (audio.rs) { audio.rs.pos = 0; audio.rs.hasPrev = false; }
        samples = src;
      }
      enqueuePcm(samples);
      n += 1;
      if (!audio.loggedPcm) {
        audio.loggedPcm = true;
        slog("pcm-out", "n=" + samples.length, "q=" + ((audio.q && audio.q.length) || 0), "qs=" + pcmQueuedSec().toFixed(3), "ctx=" + (audio.ctx ? audio.ctx.state : "none"), "port=" + !!audio.usePort);
      }
    }
    maybeKickSpeaker(st);
    return n;
  }

  function maybeKickSpeaker(st) {
    if (audio.spkLive || paused || visHold || rebuffering || !startArmed) return;
    const qs = audio.workletQs || 0;
    const have = qs >= PCM_AHEAD * 0.85;
    if (audio.usePort && audio.node && audio.node.port) {
      if (!have) return;
      startClockFromAudio(st);
      audio.node.port.postMessage({ type: "start", gen: audio.gen });
      audio.spkLive = true;
      slog("pcm-start", "qs=" + qs.toFixed(3));
      return;
    }
    startClockFromAudio(st);
    audio.spkLive = true;
  }

  function onScriptProc(ev) {
    if (audio.usePort) return;
    if (!paused && !visHold && !rebuffering && startArmed && activeId) {
      const st = pipes.get(activeId);
      if (st) pumpSpeaker(st);
    }
    const left = ev.outputBuffer.getChannelData(0);
    const right = ev.outputBuffer.getChannelData(1);
    fillPcm(left, right);
  }

  function fillPcm(left, right) {
    const q = audio.q || [];
    let i = 0;
    let starved = 0;
    while (i < left.length) {
      if (!q.length) {
        left[i] = 0;
        right[i] = 0;
        starved += 1;
        i++;
        continue;
      }
      const cur = q[0];
      if (cur.off + 1 >= cur.samples.length) {
        q.shift();
        continue;
      }
      left[i] = cur.samples[cur.off];
      right[i] = cur.samples[cur.off + 1];
      cur.off += 2;
      i++;
    }
    if (starved) {
      audio.starveN = (audio.starveN || 0) + 1;
      const t = performance.now();
      if (!audio.starveLog || t - audio.starveLog > 400) {
        audio.starveLog = t;
        logStarve("script", "cb=" + starved + " n=" + audio.starveN);
        enterRebuffer(pipes.get(activeId), "script");
      }
    }
  }

  function tryResume() {
    unlockAudio();
  }

  function syncMuteUi() {
    const silent = audio.muted || !audio.ctx || audio.blocked || audio.ctx.state !== "running";
    onMuteChange(!!silent);
  }

  function canvasOf(id) {
    const el = document.getElementById("s-" + id);
    return el ? el.querySelector("canvas.stage") : null;
  }

  function pushV(st, item) {
    st.vin = (st.vin || 0) + 1;
    const pts = Number(item && item.pts);
    const q = st.video;
    if (!q.length || !Number.isFinite(pts) || pts >= Number(q[q.length - 1].pts)) {
      q.push(item);
      return;
    }
    let i = q.length - 1;
    while (i >= 0 && Number(q[i].pts) > pts) i -= 1;
    q.splice(i + 1, 0, item);
  }
  function shiftV(st) {
    if (!st.video.length) return null;
    st.vout = (st.vout || 0) + 1;
    return st.video.shift();
  }
  function pushA(st, item) {
    const pts = Number(item && item.pts);
    const q = st.audio;
    if (!q.length || !Number.isFinite(pts) || pts >= Number(q[q.length - 1].pts)) {
      q.push(item);
    } else {
      let i = q.length - 1;
      while (i >= 0 && Number(q[i].pts) > pts) i -= 1;
      q.splice(i + 1, 0, item);
    }
    st.ain = (st.ain || 0) + 1;
  }
  function shiftA(st) {
    if (!st.audio.length) return null;
    st.aout = (st.aout || 0) + 1;
    return st.audio.shift();
  }

  function clampFrameDur(d) {
    const x = Number(d);
    if (!Number.isFinite(x) || x < DUR_MIN || x > DUR_MAX) return DUR_FALLBACK;
    return x;
  }

  function frameDur(item, lastPts) {
    const pts = Number(item && item.pts);
    const delta = pts - Number(lastPts);
    if (Number.isFinite(delta) && delta >= DUR_MIN && delta <= DUR_MAX) return delta;
    return clampFrameDur(item && item.duration);
  }

  function pipeDeep(st) {
    return !!(st && (st.video.length >= PIPE_ACCEL_N || dequeSeconds(st.video) >= PIPE_ACCEL_SEC));
  }

  function notePresent() {
    sync.lastPresentWall = wallSec();
  }

  function takeFallback(st, lastPts, dur) {
    const wall = wallSec();
    if (!sync.lastPresentWall || wall - sync.lastPresentWall >= dur) {
      const item = shiftV(st);
      if (item && Number.isFinite(lastPts) && Number(item.pts) > lastPts + HOLE_THRESH) {
        slog("pts-gap", "v", Number(item.pts).toFixed(3), "after=" + lastPts.toFixed(3));
      }
      notePresent();
      return item;
    }
    return null;
  }

  function takeVideoOne(st, lastPts) {
    while (st.video.length) {
      const pts = st.video[0].pts;
      if (!Number.isFinite(pts)) {
        closePic(shiftV(st));
        continue;
      }
      if (Number.isFinite(lastPts) && pts < lastPts - 0.001) {
        slog("drop-back", "v", pts.toFixed(3), "after=" + lastPts.toFixed(3));
        closePic(shiftV(st));
        continue;
      }
      break;
    }
    if (!st.video.length) return null;
    const head = st.video[0];
    const pts = Number(head.pts);
    const dur = frameDur(head, lastPts);
    if (!clock.primed()) return takeFallback(st, lastPts, dur);
    const now = clock.get();
    if (sync.visCatch) {
      if (pts < now - SYNC_TIGHT) {
        let due = null;
        while (st.video.length) {
          const p = st.video[0].pts;
          if (!Number.isFinite(p)) {
            closePic(shiftV(st));
            continue;
          }
          if (Number.isFinite(lastPts) && p < lastPts - 0.001) {
            closePic(shiftV(st));
            continue;
          }
          if (p > now) break;
          if (due) closePic(due);
          due = shiftV(st);
          lastPts = p;
        }
        if (due) {
          notePresent();
        }
        if (!st.video.length || Number(st.video[0].pts) >= now - SYNC_TIGHT) {
          sync.visCatch = false;
          sync.lost = false;
          sync.vDiff = 0;
          resetVideoPace();
        }
        return due;
      }
      sync.visCatch = false;
      sync.lost = false;
      sync.vDiff = 0;
      resetVideoPace();
    }
    const adelta = Math.abs(pts - now);
    if (sync.lost) {
      if (videoLeaveLost(pts, now)) {
        sync.lost = false;
        sync.vDiff = 0;
        resetVideoPace();
        slog("try-match", "v=" + pts.toFixed(3), "c=" + now.toFixed(3));
      }
    } else if (videoIsLost(pts, now)) {
      sync.lost = true;
      sync.vDiff = pts - now;
      resetVideoPace();
      slog("lost-match", "v=" + pts.toFixed(3), "c=" + now.toFixed(3), "diff=" + sync.vDiff.toFixed(3));
      notePresent();
      return shiftV(st);
    }
    if (sync.lost) {
      const virt = pts - sync.vDiff;
      if (Math.abs(virt - now) < SYNC_TIGHT) {
        if (virt < now) {
          resetVideoPace();
          if (Number.isFinite(lastPts) && pts > lastPts + HOLE_THRESH) {
            slog("pts-gap", "v", pts.toFixed(3), "after=" + lastPts.toFixed(3));
          }
          notePresent();
          return shiftV(st);
        }
        return takeFallback(st, lastPts, dur);
      }
      sync.vDiff = pts - now;
      resetVideoPace();
      slog("lost-match", "rebase", "v=" + pts.toFixed(3), "c=" + now.toFixed(3), "diff=" + sync.vDiff.toFixed(3));
      notePresent();
      return shiftV(st);
    }
    if (adelta <= SYNC_TIGHT) {
      resetVideoPace();
      if (pts >= now) return null;
      let due = null;
      while (st.video.length) {
        const p = st.video[0].pts;
        if (!Number.isFinite(p)) {
          closePic(shiftV(st));
          continue;
        }
        if (Number.isFinite(lastPts) && p < lastPts - 0.001) {
          slog("drop-back", "v", p.toFixed(3), "after=" + lastPts.toFixed(3));
          closePic(shiftV(st));
          continue;
        }
        if (p >= now) break;
        if (Number.isFinite(lastPts) && p > lastPts + HOLE_THRESH) {
          slog("pts-gap", "v", p.toFixed(3), "after=" + lastPts.toFixed(3));
        }
        if (due) closePic(due);
        due = shiftV(st);
        lastPts = p;
      }
      if (due) notePresent();
      return due;
    }
    if (pts < now) {
      const rate = pipeDeep(st) ? 2 : 1;
      if (sync.lastClock == null || now - sync.lastClock > dur / rate) {
        sync.lastClock = now;
        notePresent();
        return shiftV(st);
      }
      return null;
    }
    if (sync.lastClock == null || now - sync.lastClock > 2 * dur) {
      sync.lastClock = now;
      notePresent();
      return shiftV(st);
    }
    return null;
  }

  function fitStageBox(canvas, mw, mh) {
    const box = canvas && canvas.parentElement;
    if (!box || !box.classList.contains("stage-box") || !mw || !mh) return;
    const slide = box.closest(".slide");
    if (!slide) return;
    const maxW = slide.clientWidth || window.innerWidth;
    const maxH = slide.clientHeight || window.innerHeight;
    const s = Math.min(maxW / mw, maxH / mh);
    const w = Math.round(mw * s) + "px";
    const h = Math.round(mh * s) + "px";
    if (box.style.width === w && box.style.height === h) return;
    box.style.width = w;
    box.style.height = h;
  }

  function holdFrame(id, frame) {
    const prev = onScreen[id];
    onScreen[id] = frame;
    if (prev && prev !== frame) {
      try { prev.close(); } catch (e) {}
    }
    parked[id] = false;
  }

  function closePic(x) {
    if (!x) return;
    if (x.frame) {
      try { x.frame.close(); } catch (e) {}
      x.frame = null;
    }
    if (x.bmp) {
      try { x.bmp.close(); } catch (e) {}
      x.bmp = null;
    }
  }

  function blit(id, src, pts) {
    const sz = stageSize(id, src);
    if (!sz || !src) return false;
    const c = sz.c;
    if (c.width !== sz.dw || c.height !== sz.dh) {
      c.width = sz.dw;
      c.height = sz.dh;
    }
    c.getContext("2d").drawImage(src, 0, 0, sz.dw, sz.dh);
    primed[id] = true;
    parked[id] = false;
    if (pts != null) shownPts[id] = pts;
    markRendered(id, pts);
    return true;
  }

  function frameToBmp(id, frame) {
    if (!frame) return null;
    const sz = stageSize(id, frame);
    const dw = (sz && sz.dw) || frame.displayWidth || frame.codedWidth || 2;
    const dh = (sz && sz.dh) || frame.displayHeight || frame.codedHeight || 2;
    return snapshotFrame(frame, dw, dh);
  }

  function freezeToBitmaps(id) {
    const st = pipes.get(id);
    if (st) {
      for (let i = 0; i < st.video.length; i++) {
        const x = st.video[i];
        if (x.frame && !x.bmp) {
          x.bmp = frameToBmp(id, x.frame);
          try { x.frame.close(); } catch (e) {}
          x.frame = null;
        }
      }
    }
    const f = onScreen[id];
    if (f) {
      const bmp = frameToBmp(id, f);
      if (bmp) {
        if (idleBmp[id] && idleBmp[id] !== bmp) {
          try { idleBmp[id].close(); } catch (e) {}
        }
        idleBmp[id] = bmp;
        blit(id, bmp, shownPts[id]);
        parked[id] = true;
      }
      try { f.close(); } catch (e) {}
      delete onScreen[id];
    }
    if (st) reportHeld(id);
    return !onScreen[id];
  }

  function parkCanvas(id) {
    if (parked[id] && !onScreen[id]) return true;
    return freezeToBitmaps(id);
  }

  function dropHeld(id) {
    if (!parkCanvas(id) && onScreen[id]) return;
    const f = onScreen[id];
    if (f) {
      try { f.close(); } catch (e) {}
      delete onScreen[id];
    }
  }

  function pruneHeld(keep) {
    Object.keys(onScreen).forEach(function (id) {
      if (keep && keep.has && keep.has(id)) return;
      dropHeld(id);
    });
  }

  function draw(id, frame, pts) {
    if (!sizeAndDraw(id, frame)) {
      if (frame) {
        try { frame.close(); } catch (e) {}
      }
      return;
    }
    holdFrame(id, frame);
    primed[id] = true;
    parked[id] = false;
    if (pts != null) shownPts[id] = pts;
    markRendered(id, pts);
  }

  function sizeAndDraw(id, frame) {
    const c = canvasOf(id);
    if (!c || !frame) return false;
    fitStageBox(c, frame.displayWidth, frame.displayHeight);
    const box = c.parentElement;
    const dpr = Math.min(2, window.devicePixelRatio || 1);
    let dw = frame.displayWidth;
    let dh = frame.displayHeight;
    if (box && box.clientWidth > 8) {
      dw = Math.max(2, Math.round(box.clientWidth * dpr));
      dh = Math.max(2, Math.round(box.clientHeight * dpr));
    }
    const ctx = c.getContext("2d");
    if (parked[id] && (c.width !== dw || c.height !== dh)) {
      ctx.drawImage(frame, 0, 0, c.width, c.height);
      return true;
    }
    if (c.width !== dw || c.height !== dh) {
      c.width = dw;
      c.height = dh;
    }
    ctx.drawImage(frame, 0, 0, dw, dh);
    return true;
  }

  function stageSize(id, frame) {
    const c = canvasOf(id);
    if (!c) return null;
    const fw = (frame && (frame.displayWidth || frame.width)) || 0;
    const fh = (frame && (frame.displayHeight || frame.height)) || 0;
    if (fw && fh) fitStageBox(c, fw, fh);
    const box = c.parentElement;
    const dpr = Math.min(2, window.devicePixelRatio || 1);
    let dw = fw || c.width || 2;
    let dh = fh || c.height || 2;
    if (box && box.clientWidth > 8 && box.clientHeight > 8) {
      dw = Math.max(2, Math.round(box.clientWidth * dpr));
      dh = Math.max(2, Math.round(box.clientHeight * dpr));
    }
    return { c, dw, dh };
  }

  function snapshotFrame(frame, dw, dh) {
    try {
      if (typeof OffscreenCanvas === "function") {
        const off = new OffscreenCanvas(dw, dh);
        off.getContext("2d").drawImage(frame, 0, 0, dw, dh);
        return off.transferToImageBitmap();
      }
    } catch (e) {}
    return null;
  }

  function ingestIdle(id, frame, pts, duration) {
    const st = pipes.get(id);
    if (!st) {
      if (frame) {
        try { frame.close(); } catch (e) {}
      }
      return;
    }
    pushV(st, { pts, duration, frame: frame || null, bmp: null });
    if (id !== activeId) paintFirst(id);
    reportHeld(id);
  }

  function paintFirst(id) {
    if (parked[id] && idleBmp[id]) return blit(id, idleBmp[id], shownPts[id]);
    if (primed[id]) return true;
    const st = pipes.get(id);
    if (!st || !st.video.length) {
      if (idleBmp[id]) return blit(id, idleBmp[id], shownPts[id]);
      return false;
    }
    const first = st.video[0];
    const src = first && (first.bmp || first.frame);
    if (!src) return false;
    return blit(id, src, first.pts);
  }

  function presentIdle(id) {
    if (!id || id === activeId) return;
    if (primed[id] && !parked[id]) return;
    paintFirst(id);
  }

  function reportHeld(id) {
    const st = pipes.get(id);
    if (!st) return;
    const pair = workers.get(id);
    if (pair) pair.decode.postMessage({ type: "held", id, video: dequeRunSeconds(st.video), audio: dequeSeconds(st.audio) });
  }

  function videoHasGopHole(st) {
    if (!st || st.video.length < 2) return false;
    const first = Number(st.video[0].pts);
    const last = Number(st.video[st.video.length - 1].pts);
    return Number.isFinite(first) && Number.isFinite(last) && last - first > HOLE_THRESH && dequeRunSeconds(st.video) < ARM_VIDEO;
  }

  function dropRewindPts(st, kind, pts) {
    if (!st || !st.dropHigh) return false;
    if (Number(pts) > 2) return true;
    if (kind === "video") st.dropHighV = false;
    else st.dropHighA = false;
    if (!st.dropHighV && (st.noAudio || !st.dropHighA)) st.dropHigh = false;
    return false;
  }

  function armStart(st) {
    if (startArmed || !st) return startArmed;
    if (!st.video.length) return false;
    if (!st.noAudio && !st.audio.length) return false;
    if (!st.noAudio && dequeSeconds(st.audio) < ARM_AUDIO) return false;
    const run = dequeRunSeconds(st.video);
    const hole = videoHasGopHole(st);
    if (run < ARM_VIDEO && !hole) return false;
    audio.t0v = st.video[0].pts;
    audio.t0a = st.audio.length ? st.audio[0].pts : audio.t0v;
    startArmed = true;
    if (hole) slog("start-hole", "vr=" + run.toFixed(2), "vh=" + Number(st.video[0].pts).toFixed(3) + ".." + Number(st.video[st.video.length - 1].pts).toFixed(3), "as=" + dequeSeconds(st.audio).toFixed(2));
    slog("pcm-fill", "v=" + Number(audio.t0v).toFixed(3), "a=" + Number(audio.t0a).toFixed(3), "vs=" + dequeSeconds(st.video).toFixed(2), "vr=" + run.toFixed(2), "as=" + dequeSeconds(st.audio).toFixed(2));
    return true;
  }

  function tick() {
    raf = requestAnimationFrame(tick);
    const t0 = performance.now();
    pipes.forEach(function (_st, id) {
      if (id !== activeId) presentIdle(id);
    });
    if (!activeId) return;
    const st = pipes.get(activeId);
    if (!st) return;
    if (!paused && !visHold) {
      if (rebuffering) {
        maybeLeaveRebuffer(st);
        paintFirst(activeId);
      } else if (startArmed || armStart(st)) {
        const audioOut = pumpSpeaker(st);
        if (!st.noAudio && !audio.spkLive) {
          paintFirst(activeId);
          if (audioOut) reportHeld(activeId);
        } else {
          const vis = takeVideoOne(st, lastRenderedPts[activeId]);
          if (vis) {
            if (vis.bmp) {
              blit(activeId, vis.bmp, vis.pts);
              try { vis.bmp.close(); } catch (e) {}
              vis.bmp = null;
            } else {
              draw(activeId, vis.frame, vis.pts);
              vis.frame = null;
            }
          }
          if (vis || audioOut) reportHeld(activeId);
        }
      } else {
        paintFirst(activeId);
      }
    }
    const nowHeld = performance.now();
    if (nowHeld - heldAt >= HELD_EVERY_MS) {
      heldAt = nowHeld;
      reportHeld(activeId);
      warmIds.forEach(function (id) {
        if (id && id !== activeId) reportHeld(id);
      });
      if (prevId && prevId !== activeId && warmIds.indexOf(prevId) < 0) reportHeld(prevId);
    }
    const cost = performance.now() - t0;
    lastTickMs = cost;
    if (!tickWinAt || t0 - tickWinAt >= 1000) {
      tickMax1s = cost;
      tickWinAt = t0;
    } else if (cost > tickMax1s) {
      tickMax1s = cost;
    }
  }

  function onWorkerMessage(ev) {
    const msg = ev.data || {};
    if (msg.type === "closed") return;
    const st = pipes.get(msg.id);
    if (msg.type === "fail") {
      if (st) st.failed = true;
      onError(msg.message || "decode failed", msg.id);
      slog("fail", "seq=" + seqLabel(msg.id), "id=" + msg.id, msg.message || "decode failed");
      return;
    }
    if (msg.type === "decoder") {
      return;
    }
    if (msg.type === "log") {
      return;
    }
    if (msg.type === "pktq") {
      if (st) {
        st.pkt = {
          vpd: msg.vpd || 0,
          vps: msg.vps || 0,
          apd: msg.apd || 0,
          aps: msg.aps || 0,
          vdq: msg.vdq || 0,
          adq: msg.adq || 0,
          vpend: msg.vpend || 0
        };
        st.pktAt = performance.now();
        if (msg.dump && st.wantPktDump && msg.id === activeId && !rebuffering) {
          st.wantPktDump = false;
          logStarve("pktq", "dump=1");
        }
      }
      return;
    }
    if (!st) {
      if (msg.frame) msg.frame.close();
      return;
    }
    if (msg.type === "opened") {
      st.openedAt = performance.now();
      st.firstTimer = setTimeout(() => {
        if (!st.gotFrame && !st.failed && msg.id === activeId) {
          st.failed = true;
          onError("首帧超时", msg.id);
        }
      }, FIRST_FRAME_MS);
      return;
    }
    if (msg.type === "looped") {
      st.looped = true;
      slog("looped", "seq=" + seqLabel(msg.id), "id=" + msg.id);
      return;
    }
    if (msg.type === "noaudio") {
      st.noAudio = true;
      if (st.dropHigh) st.dropHighA = false;
      if (st.dropHigh && !st.dropHighV && !st.dropHighA) st.dropHigh = false;
      slog("noaudio", "seq=" + seqLabel(msg.id), "id=" + msg.id, msg.reason || "");
      return;
    }
    if (msg.type === "video") {
      if (st.firstTimer) {
        clearTimeout(st.firstTimer);
        st.firstTimer = 0;
        st.gotFrame = true;
      }
      const shown = lastRenderedPts[msg.id];
      if (msg.id === activeId && clock.primed() && Number.isFinite(shown) && Number(msg.pts) < shown - 0.001) {
        if (msg.bmp) {
          try { msg.bmp.close(); } catch (e) {}
        } else if (msg.frame) {
          try { msg.frame.close(); } catch (e) {}
        }
        return;
      }
      if (dropRewindPts(st, "video", msg.pts)) {
        if (msg.bmp) {
          try { msg.bmp.close(); } catch (e) {}
        } else if (msg.frame) {
          try { msg.frame.close(); } catch (e) {}
        }
        return;
      }
      if (msg.bmp) {
        pushV(st, { pts: msg.pts, duration: msg.duration, frame: null, bmp: msg.bmp });
        if (msg.id !== activeId) paintFirst(msg.id);
        reportHeld(msg.id);
        return;
      }
      if (msg.id !== activeId) {
        ingestIdle(msg.id, msg.frame, msg.pts, msg.duration);
        return;
      }
      pushV(st, { pts: msg.pts, duration: msg.duration, frame: msg.frame, bmp: null });
      reportHeld(msg.id);
      return;
    }
    if (msg.type === "audio") {
      st.noAudio = false;
      if (dropRewindPts(st, "audio", msg.pts)) return;
      pushA(st, { pts: msg.pts, duration: msg.duration, sampleRate: msg.sampleRate, samples: msg.samples });
      if (!st.gotAudioLog) {
        st.gotAudioLog = true;
        slog("audio-in", "seq=" + seqLabel(msg.id), "id=" + msg.id, "pts=" + Number(msg.pts || 0).toFixed(3), "rate=" + msg.sampleRate, "n=" + ((msg.samples && msg.samples.length) || 0));
      }
      if (msg.id === activeId && startArmed) {
        maybeLeaveRebuffer(st);
        if (!rebuffering) pumpSpeaker(st);
      }
      reportHeld(msg.id);
    }
  }

  function open(id, url, seq) {
    const cur = pipes.get(id);
    if (cur && !cur.failed) return cur;
    if (cur) close(id);
    if (Number.isFinite(seq)) seqOf[id] = seq;
    const st = {
      video: [],
      audio: [],
      vin: 0,
      vout: 0,
      ain: 0,
      aout: 0,
      noAudio: false,
      failed: false,
      gotFrame: false,
      firstTimer: 0,
      pkt: { vpd: 0, vps: 0, apd: 0, aps: 0, vdq: 0, adq: 0, vpend: 0 },
    };
    pipes.set(id, st);
    renderedCount[id] = 0;
    delete lastRenderedPts[id];
    delete shownPts[id];
    delete primed[id];
    const tok = (opts.getToken && opts.getToken()) || opts.token || "";
    st.epoch = 1;
    const demux = new Worker("/static/shorts-worker.js?v=" + workerVer, { type: "module" });
    const decode = new Worker("/static/shorts-decode-worker.js?v=" + workerVer);
    const ch = new MessageChannel();
    demux.onmessage = onWorkerMessage;
    decode.onmessage = onWorkerMessage;
    workers.set(id, { demux: demux, decode: decode });
    decode.postMessage({
      type: "open",
      id: id,
      seq: seqLabel(id),
      epoch: st.epoch,
      port: ch.port2,
      listen: activeId,
      warm: warmId,
      warms: warmIds.slice(),
      prev: prevId
    }, [ch.port2]);
    demux.postMessage({
      type: "open",
      id: id,
      url: url,
      startPts: 0,
      token: tok,
      seq: seqLabel(id),
      epoch: st.epoch,
      port: ch.port1,
      listen: activeId,
      warm: warmId,
      warms: warmIds.slice(),
      prev: prevId
    }, [ch.port1]);
    return st;
  }

  function dropIdleBmp(id) {
    const bmp = idleBmp[id];
    if (bmp) {
      try { bmp.close(); } catch (e) {}
      delete idleBmp[id];
    }
  }

  function close(id) {
    const st = pipes.get(id);
    if (!st) return;
    const held = onScreen[id];
    if (id === activeId && clock.primed()) resumeAt[id] = clock.get();
    parkCanvas(id);
    if (st.firstTimer) clearTimeout(st.firstTimer);
    st.video.forEach(function (x) {
      if (x.frame && x.frame !== held) {
        try { x.frame.close(); } catch (e) {}
      }
      if (x.bmp) {
        try { x.bmp.close(); } catch (e) {}
      }
    });
    pipes.delete(id);
    if (parked[id]) dropHeld(id);
    const pair = workers.get(id);
    workers.delete(id);
    if (pair) {
      try { pair.decode.postMessage({ type: "close", id: id }); } catch (e) {}
      try { pair.demux.postMessage({ type: "close", id: id }); } catch (e) {}
      setTimeout(function () {
        try { pair.decode.terminate(); } catch (e2) {}
        try { pair.demux.terminate(); } catch (e3) {}
      }, 30);
    }
    delete primed[id];
    delete shownPts[id];
    delete lastRenderedPts[id];
    dropIdleBmp(id);
    if (activeId === id) flushSpeaker();
  }

  function takeBackPcm(id, inf, leftoverSec) {
    const st = pipes.get(id);
    if (!st || !inf || !inf.length) return;
    let total = 0;
    for (let i = 0; i < inf.length; i++) total += pcmItemSec(inf[i]);
    let keepSec = leftoverSec;
    if (!Number.isFinite(keepSec) || keepSec < 0) keepSec = 0;
    if (keepSec > total) keepSec = total;
    let skip = total - keepSec;
    const keep = [];
    for (let i = 0; i < inf.length; i++) {
      const sec = pcmItemSec(inf[i]);
      if (skip >= sec - 1e-5) {
        skip -= sec;
        continue;
      }
      let item = inf[i];
      if (skip > 1e-5) {
        item = skipPcmItem(item, skip);
        skip = 0;
      }
      if (item) keep.push(item);
    }
    for (let i = keep.length - 1; i >= 0; i--) {
      st.audio.unshift(keep[i]);
      if (st.aout) st.aout -= 1;
    }
    if (keep.length) {
      slog("pcm-restore", "seq=" + seqLabel(id), "id=" + id, "n=" + keep.length, "sec=" + keepSec.toFixed(3));
      reportHeld(id);
    }
  }

  function flushSpeaker() {
    const owner = audio.inflightId || activeId;
    const inf = audio.inflight || [];
    audio.inflight = [];
    audio.inflightId = null;
    audio.gen += 1;
    if (!audio.usePort) {
      takeBackPcm(owner, inf, pcmQueuedSec());
    } else if (inf.length) {
      audio.drains = audio.drains || {};
      audio.drains[audio.gen] = { owner: owner, inflight: inf };
    }
    audio.sentSec = 0;
    audio.dacAt = null;
    audio.workletQs = 0;
    audio.enqEnd = null;
    audio.spkLive = false;
    audio.rs = { pos: 0, hasPrev: false };
    if (audio.node && audio.node.port) {
      audio.node.port.postMessage({ type: "reset", gen: audio.gen });
    }
    audio.q = [];
    if (audio.gain) audio.gain.gain.value = 0;
  }

  function trimBefore(st, t) {
    if (!st || !Number.isFinite(t)) return;
    while (st.video.length && st.video[0].pts + 0.001 < t) closePic(shiftV(st));
    while (st.audio.length && st.audio[0].pts + 0.001 < t) shiftA(st);
  }

  function armFromPainted(id) {
    const st = pipes.get(id);
    const painted = shownPts[id];
    clock.clear();
    resetSync();
    if (!st || !Number.isFinite(painted)) return;
    trimBefore(st, painted);
    slog("clock-resume", "seq=" + seqLabel(id), "id=" + id, painted.toFixed(3));
  }

  function dropQueuedMedia(st) {
    if (!st) return;
    st.aout = (st.aout || 0) + st.audio.length;
    st.audio.length = 0;
    while (st.video.length) closePic(shiftV(st));
  }

  function activate(id) {
    if (activeId && activeId !== id) {
      const prev = pipes.get(activeId);
      if (prev) {
        if (clock.primed()) resumeAt[activeId] = clock.get();
        freezeToBitmaps(activeId);
        reportHeld(activeId);
      }
    }
    flushSpeaker();
    visHold = false;
    rebuffering = false;
    activeId = id;
    paused = false;
    startArmed = false;
    armFromPainted(id);
    sendPriority();
    if (audio.gain && !audio.muted) audio.gain.gain.value = 1;
    if (!raf) raf = requestAnimationFrame(tick);
    unlockAudio();
  }

  function pause() {
    paused = true;
    rebuffering = false;
    clock.pause();
    flushSpeaker();
    onUi(activeId, { paused: true });
  }

  function play() {
    if (!paused) return;
    paused = false;
    if (!rebuffering) clock.play();
    unlockAudio();
    if (audio.gain && !audio.muted) audio.gain.gain.value = 1;
    if (activeId && !rebuffering) pumpSpeaker(pipes.get(activeId));
    onUi(activeId, { paused: false });
  }

  function toggle() {
    if (paused) play();
    else pause();
  }

  function setMuted(next) {
    audio.muted = !!next;
    unlockAudio();
    if (audio.gain) audio.gain.gain.value = audio.muted ? 0 : 1;
    if (!audio.muted && audio.ctx && audio.ctx.state === "suspended") {
      try { audio.ctx.resume(); } catch (e) {}
    }
    audio.blocked = !!(audio.ctx && audio.ctx.state !== "running");
    syncMuteUi();
  }

  function isMuted() { return !!audio.muted; }

  function isSilent() {
    if (audio.muted) return true;
    if (!audio.ctx || audio.blocked || audio.ctx.state !== "running") return true;
    return !(audio.q && audio.q.length);
  }

  function sendPriority() {
    workers.forEach(function (pair) {
      const msg = { type: "priority", listen: activeId, warm: warmId, warms: warmIds.slice(), prev: prevId };
      try { pair.demux.postMessage(msg); } catch (e) {}
      try { pair.decode.postMessage(msg); } catch (e2) {}
    });
  }

  function applyWarms(next) {
    if (Array.isArray(next)) warmIds = next.filter(Boolean);
    else warmIds = next ? [next] : [];
    warmId = warmIds[0] || null;
  }

  function setWarm(id) {
    applyWarms(id || null);
    sendPriority();
  }

  function setHotWindow(prev, next) {
    prevId = prev || null;
    applyWarms(next);
    sendPriority();
  }

  function isPaused() { return paused; }

  function listenReady() {
    const st = pipes.get(activeId);
    if (!st || st.failed) return false;
    if (dequeSeconds(st.video) < ARM_VIDEO) return false;
    if (st.noAudio) return true;
    return dequeSeconds(st.audio) >= ARM_AUDIO;
  }

  function neighborsReady() {
    const st = pipes.get(activeId);
    if (!st || st.failed) return false;
    if (st.gotFrame) return true;
    return dequeSeconds(st.video) >= ARM_VIDEO;
  }

  function listenStarved() {
    const st = pipes.get(activeId);
    return !!(st && st.gotFrame && st.video.length === 0);
  }

  function holdForBackground() {
    if (visHold) return;
    visHold = true;
    rebuffering = false;
    clock.pause();
    flushSpeaker();
    slog("vis-hold");
  }

  function resumeFromBackground() {
    if (!visHold) return;
    visHold = false;
    if (paused) return;
    if (rebuffering) return;
    clock.play();
    sync.lost = false;
    sync.vDiff = 0;
    sync.visCatch = true;
    resetVideoPace();
    unlockAudio();
    if (audio.gain && !audio.muted) audio.gain.gain.value = 1;
    if (activeId) pumpSpeaker(pipes.get(activeId));
    slog("vis-resume");
  }

  document.addEventListener("click", () => { unlockAudio(); }, true);
  document.addEventListener("pointerdown", () => { unlockAudio(); }, true);
  document.addEventListener("keydown", () => { unlockAudio(); }, true);
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState !== "visible") holdForBackground();
    else resumeFromBackground();
  });
  window.addEventListener("pagehide", holdForBackground);
  window.addEventListener("pageshow", resumeFromBackground);

  return {
    open, close, activate, pause, play, toggle, setMuted, isPaused, isMuted, setWarm, setHotWindow, unlockAudio, isSilent, pruneHeld, presentIdle, logHot, listenReady, neighborsReady, listenStarved, mediaTime: function () {
      const shown = shownPts[activeId];
      if (Number.isFinite(shown)) return shown;
      if (clock.primed()) return clock.get();
      return 0;
    },
    has: (id) => {
      const st = pipes.get(id);
      return !!(st && !st.failed);
    },
    failed: (id) => !!(pipes.get(id) && pipes.get(id).failed),
    destroy() {
      [...pipes.keys()].forEach(close);
      Object.keys(onScreen).forEach(dropHeld);
      Object.keys(idleBmp).forEach(dropIdleBmp);
      workers.forEach(function (pair) {
        try { pair.decode.terminate(); } catch (e) {}
        try { pair.demux.terminate(); } catch (e2) {}
      });
      workers.clear();
      if (raf) cancelAnimationFrame(raf);
      if (audio.ctx) audio.ctx.close();
    }
  };
}
