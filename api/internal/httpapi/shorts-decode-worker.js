const LISTEN_BUDGET = 0.5;
const LISTEN_AUDIO_BUDGET = 0.85;
const PIPE_SLACK = 0.25;
const IDLE_BUDGET = 0.25;
const FILL_SLACK = 0.02;
const LISTEN_QUEUE = 8;
const GOP_QUEUE = 4;
const SNAP_QUEUE = 8;
const IDLE_SLEEP_MS = 10;
const V_DUR_EST = 1 / 30;
const A_DUR_EST = 1024 / 44100;
const PKT_BUDGET = 3;
const VPD_MAX = 180;
const APD_MAX = 130;
const PUMP_STEPS = 16;
const V_DUR_MIN = 1 / 120;
const V_DUR_MAX = 0.1;
const A_DUR_MIN = 0.015;
const A_DUR_MAX = 0.08;

let listenId = null;
let warmId = null;
let warmIds = [];
let prevId = null;
let pipe = null;
let port = null;
let loopOn = false;

function isHot(id) {
  return id && (id === listenId || id === warmId || id === prevId || warmIds.indexOf(id) >= 0);
}

function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}

function audioToStereo(data) {
  const n = data.numberOfFrames;
  const rate = data.sampleRate;
  const ch = Math.max(1, data.numberOfChannels || 1);
  const pts = (data.timestamp || 0) / 1e6;
  const duration = n / rate;
  const interleaved = new Float32Array(n * 2);
  const fmt = String(data.format || "");
  try {
    if (fmt.indexOf("planar") >= 0 || !fmt) {
      const left = new Float32Array(n);
      data.copyTo(left, { planeIndex: 0 });
      let right = left;
      if (ch > 1) {
        right = new Float32Array(n);
        data.copyTo(right, { planeIndex: 1 });
      }
      for (let i = 0; i < n; i++) {
        interleaved[i * 2] = left[i];
        interleaved[i * 2 + 1] = right[i];
      }
    } else {
      const packed = new Float32Array(n * ch);
      data.copyTo(packed, { planeIndex: 0 });
      for (let i = 0; i < n; i++) {
        interleaved[i * 2] = packed[i * ch];
        interleaved[i * 2 + 1] = ch > 1 ? packed[i * ch + 1] : packed[i * ch];
      }
    }
  } finally {
    try { data.close(); } catch (e) {}
  }
  return { pts, duration, sampleRate: rate, samples: interleaved };
}

function copyFrameBitmap(frame, maxH) {
  const srcW = Math.max(2, frame.displayWidth || 2);
  const srcH = Math.max(2, frame.displayHeight || 2);
  const scale = maxH > 0 ? Math.min(1, maxH / srcH) : 1;
  const w = Math.max(2, Math.round(srcW * scale));
  const h = Math.max(2, Math.round(srcH * scale));
  const off = new OffscreenCanvas(w, h);
  off.getContext("2d").drawImage(frame, 0, 0, w, h);
  try { frame.close(); } catch (e) {}
  return off.transferToImageBitmap();
}

function takeVideoPts(p, frame) {
  const q = p.vPtsQ || [];
  const hw = (frame.timestamp || 0) / 1e6;
  function recDur(rec) {
    return clampDur("video", rec && rec.dur);
  }
  // B-frames leave decode order; match the in-flight packet whose PTS
  // we stamped on EncodedVideoChunk, not FIFO submit order.
  if (q.length && Number.isFinite(hw) && hw > 0) {
    let best = 0;
    let bestD = Math.abs(Number(q[0].pts) - hw);
    for (let i = 1; i < q.length; i++) {
      const d = Math.abs(Number(q[i].pts) - hw);
      if (d < bestD) {
        bestD = d;
        best = i;
      }
    }
    if (bestD < 0.05) {
      const rec = q.splice(best, 1)[0];
      return { pts: rec.pts, duration: recDur(rec) };
    }
  }
  if (q.length) {
    const rec = q.shift();
    return { pts: rec.pts, duration: recDur(rec) };
  }
  return {
    pts: hw,
    duration: clampDur("video", (frame.duration || 33333) / 1e6),
  };
}

function emitVideo(p, frame) {
  if (p.abort) {
    frame.close();
    return;
  }
  if ((p.dropVideo || 0) > 0) {
    p.dropVideo -= 1;
    frame.close();
    return;
  }
  const idle = !!(listenId && p.id !== listenId);
  const stamp = takeVideoPts(p, frame);
  const pts = stamp.pts;
  const duration = stamp.duration;
  if (p.rawFrames) {
    p.firstSnap = true;
    self.postMessage({ type: "video", id: p.id, pts, duration, frame }, [frame]);
    return;
  }
  const maxH = (Number(p.maxH) > 0) ? p.maxH : (idle ? 540 : 0);
  if (idle && p.firstSnap) {
    try {
      const bmp = copyFrameBitmap(frame, maxH || 0);
      self.postMessage({ type: "video", id: p.id, pts, duration, bmp }, [bmp]);
    } catch (e) {
      try { frame.close(); } catch (e2) {}
    }
    return;
  }
  p.firstSnap = true;
  try {
    const bmp = copyFrameBitmap(frame, maxH);
    self.postMessage({ type: "video", id: p.id, pts, duration, bmp }, [bmp]);
  } catch (e) {
    self.postMessage({ type: "video", id: p.id, pts, duration, frame }, [frame]);
  }
}

function clampDur(kind, d) {
  if (kind === "video") {
    if (!Number.isFinite(d) || d <= 0) return V_DUR_EST;
    if (d < V_DUR_MIN) return V_DUR_MIN;
    if (d > V_DUR_MAX) return V_DUR_MAX;
    return d;
  }
  if (!Number.isFinite(d) || d <= 0) return A_DUR_EST;
  if (d < A_DUR_MIN) return A_DUR_MIN;
  if (d > A_DUR_MAX) return A_DUR_MAX;
  return d;
}

function PacketDeque(kind) {
  this.kind = kind;
  this.items = [];
  this.held = 0;
  this.maxN = kind === "video" ? VPD_MAX : APD_MAX;
}

PacketDeque.prototype.full = function () {
  return this.items.length >= this.maxN || this.held >= PKT_BUDGET - FILL_SLACK;
};

PacketDeque.prototype.push = function (pkt, pts) {
  const dur = clampDur(this.kind, pkt && pkt.duration);
  this.items.push({ pkt: pkt, dur: dur, pts: pts });
  this.held += dur;
};

PacketDeque.prototype.shift = function () {
  const x = this.items.shift();
  if (!x) return null;
  this.held -= x.dur;
  if (this.held < 0) this.held = 0;
  return x;
};

PacketDeque.prototype.clear = function () {
  this.items = [];
  this.held = 0;
};

function decoderOf(p, kind) {
  return kind === "video" ? p.vdec : p.adec;
}

function kindHeld(p, kind) {
  return kind === "video" ? (p.videoHeld || 0) : (p.audioHeld || 0);
}

function decoderReady(p, kind) {
  const decoder = decoderOf(p, kind);
  return !!(decoder && decoder.state !== "closed" && decoder.state !== "unconfigured");
}

function canSubmit(p, kind) {
  if (!p || p.abort) return false;
  if (kind === "audio" && !p.adec) return false;
  if (!decoderReady(p, kind)) return false;
  const decoder = decoderOf(p, kind);
  const idle = !!(listenId && p.id !== listenId);
  const maxQ = !idle ? LISTEN_QUEUE : (p.firstSnap ? GOP_QUEUE : SNAP_QUEUE);
  return (decoder.decodeQueueSize || 0) < maxQ;
}

function queueHeld(p, kind) {
  const dec = decoderOf(p, kind);
  const est = kind === "video" ? V_DUR_EST : A_DUR_EST;
  return kindHeld(p, kind) + (dec ? (dec.decodeQueueSize || 0) : 0) * est;
}

function frameBudgetOf(p, kind) {
  if (listenId && p.id !== listenId) return IDLE_BUDGET;
  if (kind === "audio") return LISTEN_AUDIO_BUDGET;
  return LISTEN_BUDGET;
}

function wantSubmit(p, kind) {
  if (!canSubmit(p, kind)) return false;
  const dq = kind === "video" ? p.vpd : p.apd;
  if (!dq || !dq.items.length) return false;
  return queueHeld(p, kind) < frameBudgetOf(p, kind) + PIPE_SLACK;
}

function framesFull(p) {
  if (!p || p.abort) return true;
  const idle = !!(listenId && p.id !== listenId);
  if (idle && !p.firstSnap) return false;
  // LISTEN_BUDGET + PIPE_SLACK keeps the HW pipeline above the held budget.
  const vFull = queueHeld(p, "video") >= frameBudgetOf(p, "video") + PIPE_SLACK - FILL_SLACK;
  const aFull = !p.adec || queueHeld(p, "audio") >= frameBudgetOf(p, "audio") + PIPE_SLACK - FILL_SLACK;
  return vFull && aFull;
}

function creditOf(dq) {
  if (!dq || dq.full()) return 0;
  return Math.max(1, Math.min(8, dq.maxN - dq.items.length));
}

function sendCredit(p) {
  if (!port || !p || p.abort) return;
  try {
    port.postMessage({
      type: "credit",
      video: creditOf(p.vpd),
      audio: p.adec ? creditOf(p.apd) : 0,
    });
  } catch (e) {}
}

function postPktq(p, force) {
  if (!p) return;
  const now = (typeof performance !== "undefined" && performance.now) ? performance.now() : 0;
  if (!force && p._pktqAt && now - p._pktqAt < 200) return;
  p._pktqAt = now;
  self.postMessage({
    type: "pktq",
    id: p.id,
    dump: force ? 1 : 0,
    vpd: p.vpd ? p.vpd.items.length : 0,
    vps: p.vpd ? p.vpd.held : 0,
    apd: p.apd ? p.apd.items.length : 0,
    aps: p.apd ? p.apd.held : 0,
    vdq: p.vdec ? (p.vdec.decodeQueueSize || 0) : 0,
    adq: p.adec ? (p.adec.decodeQueueSize || 0) : 0,
    vpend: p.videoPend || 0,
  });
}

function dropLeadingDelta(p) {
  // Only before the cover key. After a key is queued/sent, remaining
  // items are GOP P-frames and must stay for playback.
  if (!p || p.sentVideoKey || p.firstSnap || (p.videoPend || 0) > 0) return;
  const dq = p.vpd;
  let n = 0;
  while (dq && dq.items.length) {
    const t = dq.items[0].pkt && dq.items[0].pkt.type;
    if (t !== "delta") break;
    dq.shift();
    n += 1;
  }
  if (n) {
    sendCredit(p);
    self.postMessage({ type: "log", id: p.id, tag: "drop-lead", message: "n=" + n });
  }
}

function encodeChunk(kind, pkt, absPts) {
  const ts = Number.isFinite(absPts) ? absPts : 0;
  const init = {
    data: pkt.data,
    type: pkt.type,
    timestamp: Math.round(ts * 1e6),
    duration: pkt.durationUs
  };
  return kind === "video" ? new EncodedVideoChunk(init) : new EncodedAudioChunk(init);
}

function sendPacket(p, kind) {
  if (!canSubmit(p, kind)) return false;
  const dq = kind === "video" ? p.vpd : p.apd;
  if (!dq || !dq.items.length) return false;
  const decoder = decoderOf(p, kind);
  const item = dq.items[0];
  if (kind === "video") {
    const dur = Number(item.dur);
    p.vPtsQ = p.vPtsQ || [];
    // enqueue pts before decode(); Chrome may output synchronously
    p.vPtsQ.push({
      pts: Number.isFinite(item.pts) ? item.pts : 0,
      dur: Number.isFinite(dur) ? dur : V_DUR_EST,
    });
    if (item.pkt && item.pkt.type === "key") p.sentVideoKey = true;
  }
  try {
    decoder.decode(encodeChunk(kind, item.pkt, item.pts));
  } catch (e) {
    if (kind === "video" && p.vPtsQ && p.vPtsQ.length) p.vPtsQ.pop();
    return false;
  }
  dq.shift();
  sendCredit(p);
  if (kind === "video") {
    if (listenId && p.id !== listenId) p.idleVideoArmed = true;
    p.videoPend = (p.videoPend || 0) + 1;
  } else {
    p.audioPend = (p.audioPend || 0) + 1;
  }
  return true;
}

function submitBudget(p) {
  let n = 0;
  while (n < PUMP_STEPS && !p.abort) {
    const wantV = wantSubmit(p, "video");
    const wantA = wantSubmit(p, "audio");
    if (!wantV && !wantA) break;
    const vNeed = frameBudgetOf(p, "video") + PIPE_SLACK - queueHeld(p, "video");
    const aNeed = frameBudgetOf(p, "audio") + PIPE_SLACK - queueHeld(p, "audio");
    let sent = false;
    if (wantA && (!wantV || aNeed > vNeed)) sent = sendPacket(p, "audio");
    else sent = sendPacket(p, "video");
    if (!sent) break;
    n += 1;
  }
  return n;
}

async function runDecode() {
  if (loopOn) return;
  loopOn = true;
  while (loopOn && pipe && !pipe.abort) {
    const p = pipe;
    dropLeadingDelta(p);
    let did = false;
    let waitHw = false;
    if (!p.firstSnap) {
      // Keep feeding the cover GOP until the first frame outputs; some HW
      // hold the IDR until more packets are queued. One packet per pump so
      // VideoDecoder output can run between submits.
      did = sendPacket(p, "video");
      if (!did && canSubmit(p, "video") && p.vpd && p.vpd.items.length) did = sendPacket(p, "video");
      if (!p.firstSnap) waitHw = !!(p.videoPend || (p.vpd && p.vpd.items.length));
    } else {
      did = submitBudget(p) > 0;
      if (!did && !framesFull(p) && (
        (p.vpd && p.vpd.items.length && canSubmit(p, "video"))
        || (p.apd && p.apd.items.length && canSubmit(p, "audio"))
      )) waitHw = true;
    }
    postPktq(p);
    sendCredit(p);
    if (did && !framesFull(p)) continue;
    if (waitHw) {
      await sleep(0);
      continue;
    }
    await sleep(IDLE_SLEEP_MS);
  }
  loopOn = false;
}

function fail(p, msg) {
  self.postMessage({ type: "fail", id: p.id, message: String(msg || "decode failed") });
}

async function configureVideo(p, cfg) {
  let vcfgUse = Object.assign({}, cfg, { optimizeForLatency: true });
  if (p.preferHardware) vcfgUse.hardwareAcceleration = "prefer-hardware";
  let okv = await VideoDecoder.isConfigSupported(vcfgUse);
  if (!okv || !okv.supported) {
    vcfgUse = Object.assign({}, cfg, { optimizeForLatency: true });
    okv = await VideoDecoder.isConfigSupported(vcfgUse);
  }
  if (!okv || !okv.supported) {
    vcfgUse = cfg;
    okv = await VideoDecoder.isConfigSupported(vcfgUse);
  }
  if (!okv || !okv.supported) throw new Error("H.264 config not supported");
  if (p.vdec) {
    try { p.vdec.close(); } catch (e) {}
  }
  p.vdec = new VideoDecoder({
    output: (frame) => {
      p.videoPend = Math.max(0, (p.videoPend || 0) - 1);
      emitVideo(p, frame);
    },
    error: (e) => fail(p, e && e.message),
  });
  p.vdec.configure(vcfgUse);
  self.postMessage({ type: "decoder", action: "open", kind: "video", id: p.id, seq: p.seq });
}

async function configureAudio(p, cfg) {
  const oka = await AudioDecoder.isConfigSupported(cfg);
  if (!oka || !oka.supported) throw new Error("AAC config not supported");
  if (p.adec) {
    try { p.adec.close(); } catch (e) {}
  }
  p.adec = new AudioDecoder({
    output: (data) => {
      p.audioPend = Math.max(0, (p.audioPend || 0) - 1);
      if ((p.dropAudio || 0) > 0) {
        p.dropAudio -= 1;
        data.close();
        return;
      }
      if (p.abort) {
        data.close();
        return;
      }
      try {
        const pkt = audioToStereo(data);
        self.postMessage({
          type: "audio",
          id: p.id,
          pts: pkt.pts,
          duration: pkt.duration,
          sampleRate: pkt.sampleRate,
          samples: pkt.samples,
        }, [pkt.samples.buffer]);
      } catch (e) {
        try { data.close(); } catch (e2) {}
        if (!p.audioCopyErr) {
          p.audioCopyErr = true;
          self.postMessage({
            type: "decoder",
            action: "error",
            kind: "audio",
            id: p.id,
            seq: p.seq,
            message: String((e && e.message) || e),
          });
        }
      }
    },
    error: (e) => {
      self.postMessage({
        type: "decoder",
        action: "error",
        kind: "audio",
        id: p.id,
        seq: p.seq,
        message: String((e && e.message) || e),
      });
    },
  });
  p.adec.configure(cfg);
  self.postMessage({ type: "decoder", action: "open", kind: "audio", id: p.id, seq: p.seq });
}

function pushPkt(p, msg) {
  if (!p || p.abort) return;
  if ((Number(msg.epoch) || 0) !== (p.epoch || 0)) return;
  const kind = msg.kind === "audio" ? "audio" : "video";
  const dq = kind === "video" ? p.vpd : p.apd;
  if (!dq) return;
  dq.push({
    data: msg.data,
    type: msg.pktType,
    duration: msg.duration,
    durationUs: msg.durationUs,
  }, Number(msg.pts));
  sendCredit(p);
}

function onDemux(ev) {
  const msg = ev.data || {};
  if (!pipe) return;
  if (msg.type === "pkt") {
    pushPkt(pipe, msg);
    return;
  }
  if (msg.type === "vconfig") {
    if ((Number(msg.epoch) || 0) !== (pipe.epoch || 0)) return;
    configureVideo(pipe, msg.config).catch((e) => fail(pipe, e && e.message));
    return;
  }
  if (msg.type === "aconfig") {
    if ((Number(msg.epoch) || 0) !== (pipe.epoch || 0)) return;
    if (pipe.dropAudio) {
      pipe.adec = null;
      sendCredit(pipe);
      self.postMessage({ type: "noaudio", id: pipe.id, reason: "drop-audio" });
      return;
    }
    configureAudio(pipe, msg.config).catch((e) => {
      self.postMessage({ type: "noaudio", id: pipe.id, reason: String((e && e.message) || e) });
      pipe.adec = null;
      sendCredit(pipe);
    });
    return;
  }
  if (msg.type === "noaudio") {
    pipe.adec = null;
    sendCredit(pipe);
  }
}

function bindPort(next) {
  port = next;
  if (!port) return;
  port.onmessage = onDemux;
}

function applyPriority(msg) {
  listenId = msg.listen || null;
  warmIds = Array.isArray(msg.warms) ? msg.warms.filter(Boolean) : (msg.warm ? [msg.warm] : []);
  warmId = warmIds[0] || null;
  prevId = msg.prev || null;
}

function rewindPipe(p, epoch) {
  p.epoch = Number(epoch) || 0;
  const vq = p.vdec ? (p.vdec.decodeQueueSize || 0) : 0;
  const aq = p.adec ? (p.adec.decodeQueueSize || 0) : 0;
  p.dropVideo = (p.videoPend || 0) + vq + 8;
  p.dropAudio = (p.audioPend || 0) + aq + 8;
  p.videoPend = 0;
  p.audioPend = 0;
  p.firstSnap = false;
  p.sentVideoKey = false;
  if (p.vpd) p.vpd.clear();
  if (p.apd) p.apd.clear();
  p.vPtsQ = [];
  try { p.vdec && p.vdec.flush(); } catch (e) {}
  try { p.adec && p.adec.flush(); } catch (e) {}
  sendCredit(p);
  self.postMessage({ type: "log", id: p.id, tag: "rewind", message: "dropV=" + p.dropVideo + " dropA=" + p.dropAudio });
}

function closePipe(p) {
  if (!p) return;
  p.abort = true;
  loopOn = false;
  if (p.vdec) {
    self.postMessage({ type: "decoder", action: "close", kind: "video", id: p.id, seq: p.seq });
  }
  if (p.adec) {
    self.postMessage({ type: "decoder", action: "close", kind: "audio", id: p.id, seq: p.seq });
  }
  try { p.vdec && p.vdec.close(); } catch (e) {}
  try { p.adec && p.adec.close(); } catch (e) {}
  p.vdec = null;
  p.adec = null;
  if (p.vpd) p.vpd.clear();
  if (p.apd) p.apd.clear();
  p.vPtsQ = [];
  try { port && port.close && port.close(); } catch (e) {}
  port = null;
  self.postMessage({ type: "closed", id: p.id, role: "decode" });
}

function makePipe(msg) {
  return {
    id: msg.id,
    abort: false,
    videoHeld: 0,
    audioHeld: 0,
    videoPend: 0,
    audioPend: 0,
    firstSnap: false,
    sentVideoKey: false,
    idleVideoArmed: false,
    vPtsQ: [],
    vpd: new PacketDeque("video"),
    apd: new PacketDeque("audio"),
    epoch: Number(msg.epoch) || 0,
    seq: Number.isFinite(Number(msg.seq)) ? Number(msg.seq) : -1,
    dropAudio: !!msg.dropAudio,
    rawFrames: !!msg.rawFrames,
    maxH: Math.max(0, Number(msg.maxH) || 0),
    preferHardware: !!msg.preferHardware,
  };
}

self.onmessage = (ev) => {
  const msg = ev.data || {};
  if (msg.type === "open") {
    applyPriority(msg);
    if (msg.port) bindPort(msg.port);
    if (pipe) closePipe(pipe);
    pipe = makePipe(msg);
    sendCredit(pipe);
    runDecode();
    return;
  }
  if (msg.type === "rewind") {
    if (!pipe || pipe.abort) return;
    rewindPipe(pipe, msg.epoch);
    return;
  }
  if (msg.type === "listen") {
    listenId = msg.id || null;
    return;
  }
  if (msg.type === "priority") {
    applyPriority(msg);
    return;
  }
  if (msg.type === "held") {
    if (!pipe || pipe.id !== msg.id) return;
    pipe.videoHeld = Number(msg.video) || 0;
    pipe.audioHeld = Number(msg.audio) || 0;
    return;
  }
  if (msg.type === "dumpq") {
    if (!pipe || pipe.id !== msg.id) return;
    postPktq(pipe, true);
    return;
  }
  if (msg.type === "close") {
    if (pipe) {
      closePipe(pipe);
      pipe = null;
    }
  }
};
