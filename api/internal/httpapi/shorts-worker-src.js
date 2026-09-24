import { Input, UrlSource, HLS_FORMATS, EncodedPacketSink } from "mediabunny";

let listenId = null;
let warmId = null;
let warmIds = [];
let prevId = null;
let authToken = "";
let pipe = null;
let port = null;
let vCredit = 8;
let aCredit = 8;
let loopOn = false;

function isHot(id) {
  return id && (id === listenId || id === warmId || id === prevId || warmIds.indexOf(id) >= 0);
}

function mediaInit() {
  const headers = {};
  if (authToken) headers.Authorization = "Bearer " + authToken;
  return { headers, credentials: "same-origin", cache: "no-store" };
}

function codecKind(c) {
  const s = String(c || "").toLowerCase();
  if (s.startsWith("avc1") || s.startsWith("avc3") || s.includes("avc1.") || s === "avc") return "avc";
  if (s.startsWith("hev1") || s.startsWith("hvc1") || s.includes("hvc1") || s.includes("hev1")) return "hevc";
  if (s.startsWith("av01") || s.includes("av1")) return "av1";
  return s;
}

async function pickMediaPlaylist(masterUrl) {
  if (!/master\.m3u8/i.test(masterUrl)) return masterUrl;
  const r = await fetch(masterUrl, mediaInit());
  if (!r.ok) throw new Error("playlist HTTP " + r.status);
  const text = await r.text();
  const lines = text.split(/\r?\n/);
  let best = null;
  let bestH = -1;
  let bestB = -1;
  let pendingH = -1;
  let pendingB = 0;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i].trim();
    if (!line || line.startsWith("#EXTM3U")) continue;
    if (line.startsWith("#EXT-X-STREAM-INF:")) {
      const hm = line.match(/RESOLUTION=\d+x(\d+)/i);
      const bm = line.match(/BANDWIDTH=(\d+)/i);
      pendingH = hm ? parseInt(hm[1], 10) : 0;
      pendingB = bm ? parseInt(bm[1], 10) : 0;
      continue;
    }
    if (line.startsWith("#")) continue;
    if (pendingH >= 0) {
      if (pendingH > bestH || (pendingH === bestH && pendingB >= bestB)) {
        bestH = pendingH;
        bestB = pendingB;
        best = line;
      }
      pendingH = -1;
      pendingB = 0;
    }
  }
  if (!best) return masterUrl;
  return new URL(best, masterUrl).href;
}

const IDLE_SLEEP_MS = 10;
const V_DUR_EST = 1 / 30;
const A_DUR_EST = 1024 / 44100;
const V_DUR_MIN = 1 / 120;
const V_DUR_MAX = 0.1;
const A_DUR_MIN = 0.015;
const A_DUR_MAX = 0.08;

function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}

function cloneConfig(cfg) {
  if (!cfg) return cfg;
  const out = Object.assign({}, cfg);
  const d = cfg.description;
  if (!d) return out;
  if (d instanceof ArrayBuffer) {
    out.description = d.slice(0);
  } else {
    const u8 = d instanceof Uint8Array ? d : new Uint8Array(d.buffer, d.byteOffset, d.byteLength);
    out.description = u8.slice().buffer;
  }
  return out;
}

function copyPktData(pkt) {
  const src = pkt && pkt.data;
  if (!src) return new ArrayBuffer(0);
  if (src instanceof ArrayBuffer) return src.slice(0);
  const u8 = src instanceof Uint8Array ? src : new Uint8Array(src.buffer, src.byteOffset, src.byteLength);
  return u8.slice().buffer;
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

function canPull(p, kind) {
  if (!p || p.abort) return false;
  if (!p.asink) return kind === "video";
  return true;
}

async function openIter(p, kind) {
  const sink = kind === "video" ? p.vsink : p.asink;
  if (!sink) return null;
  const startPts = p.startPts || 0;
  let it;
  if (startPts > 0.4 && typeof sink.getKeyPacket === "function") {
    const key = await sink.getKeyPacket(startPts);
    it = key ? sink.packets(key) : sink.packets();
  } else {
    it = sink.packets();
  }
  return it[Symbol.asyncIterator] ? it[Symbol.asyncIterator]() : it;
}

function absPktPts(p, pkt, kind) {
  const ts = Number(pkt && pkt.timestamp);
  const local = Number.isFinite(ts) ? ts : (Number(pkt && pkt.microsecondTimestamp) || 0) / 1e6;
  const base = kind === "video" ? (p.vBase || 0) : (p.aBase || 0);
  return local + base;
}

function notePktEnd(p, kind, pkt) {
  const end = Number(pkt && pkt.timestamp) + Number(pkt && pkt.duration);
  if (!Number.isFinite(end) || end < 0) return;
  if (kind === "video") {
    if (end > (p.vLastEnd || 0)) p.vLastEnd = end;
  } else if (end > (p.aLastEnd || 0)) p.aLastEnd = end;
}

async function wrapTrack(p, kind) {
  const epoch = p.epoch;
  if (p.id !== listenId) {
    if (kind === "video") p.videoEos = true;
    else p.audioEos = true;
    if (!p._idleEosLog) {
      p._idleEosLog = true;
      self.postMessage({ type: "log", id: p.id, tag: "idle-eos", message: kind });
    }
    return false;
  }
  const flag = kind === "video" ? "_vwrap" : "_awrap";
  if (p[flag] || p.abort) return false;
  p[flag] = true;
  try {
    if (kind === "video" && (p.vLastEnd || 0) < 0.08 && !(p.vBase > 0)) {
      self.postMessage({ type: "log", id: p.id, tag: "wrap-skip", message: "empty-held" });
      p.viter = null;
      p.videoEos = false;
      p.vLastEnd = 0;
      p.startPts = 0;
      const retry = await openIter(p, kind);
      if (!retry || p.abort || p.epoch !== epoch) return false;
      p.viter = retry;
      return true;
    }
    if (kind === "video") {
      p.vBase = (p.vBase || 0) + (p.vLastEnd || 0);
      p.vLastEnd = 0;
      p.viter = null;
      p.videoEos = false;
      p.videoLoop += 1;
    } else {
      p.aBase = (p.aBase || 0) + (p.aLastEnd || 0);
      p.aLastEnd = 0;
      p.aiter = null;
      p.audioEos = false;
      p.audioLoop += 1;
    }
    p.startPts = 0;
    const iter = await openIter(p, kind);
    if (!iter || p.abort || p.epoch !== epoch) return false;
    if (kind === "video") p.viter = iter;
    else p.aiter = iter;
    if (kind === "video") {
      self.postMessage({ type: "looped", id: p.id });
      self.postMessage({ type: "log", id: p.id, tag: "loop-wrap", message: "v=" + p.videoLoop + " a=" + p.audioLoop });
    }
    return true;
  } finally {
    p[flag] = false;
  }
}

function idleVideoCapped(p) {
  return !!(listenId && p.id !== listenId && (p.vLastEnd || 0) > 2);
}

function wantDemux(p, kind) {
  if (!canPull(p, kind)) return false;
  if (kind === "video") {
    if (p.videoEos || vCredit <= 0) return false;
    if (idleVideoCapped(p)) return false;
    return true;
  }
  if (p.dropAudio) return false;
  if (p.audioEos || aCredit <= 0) return false;
  return true;
}

function sendPkt(p, kind, pkt, pts) {
  if (!port || p.abort) return false;
  const data = copyPktData(pkt);
  const ts = Number.isFinite(pts) ? pts : absPktPts(p, pkt, kind);
  const dur = clampDur(kind, pkt && pkt.duration);
  try {
    port.postMessage({
      type: "pkt",
      kind: kind,
      epoch: p.epoch || 0,
      pts: ts,
      duration: dur,
      pktType: pkt && pkt.type,
      timestamp: Math.round(ts * 1e6),
      durationUs: pkt && pkt.microsecondDuration,
      data: data,
    }, [data]);
  } catch (e) {
    return false;
  }
  if (kind === "video") vCredit = Math.max(0, vCredit - 1);
  else aCredit = Math.max(0, aCredit - 1);
  return true;
}

async function demuxOne(p, kind) {
  if (!wantDemux(p, kind) || p.abort) return false;
  const epoch = p.epoch;
  let iter = kind === "video" ? p.viter : p.aiter;
  if (!iter) {
    iter = await openIter(p, kind);
    if (p.epoch !== epoch || p.abort) return false;
    if (kind === "video") p.viter = iter;
    else p.aiter = iter;
  }
  if (!iter || p.abort) return false;
  let step = await iter.next();
  if (p.epoch !== epoch || p.abort) return false;
  if ((kind === "video" ? p.viter : p.aiter) !== iter) return false;
  if (step.done) {
    if (!await wrapTrack(p, kind)) return false;
    if (p.epoch !== epoch || p.abort) return false;
    iter = kind === "video" ? p.viter : p.aiter;
    if (!iter || p.abort) return false;
    step = await iter.next();
    if (p.epoch !== epoch || p.abort) return false;
    if ((kind === "video" ? p.viter : p.aiter) !== iter) return false;
    if (step.done) return false;
  }
  notePktEnd(p, kind, step.value);
  // stamp abs pts at demux so wrap does not restamp queued packets
  return sendPkt(p, kind, step.value, absPktPts(p, step.value, kind));
}

async function runDemux() {
  if (loopOn) return;
  loopOn = true;
  while (loopOn && pipe && !pipe.abort) {
    const p = pipe;
    let did = false;
    try {
      const listen = !!(listenId && p.id === listenId);
      if (listen && aCredit > 0) {
        if (await demuxOne(p, "audio")) did = true;
        if (await demuxOne(p, "video")) did = true;
      } else {
        if (await demuxOne(p, "video")) did = true;
        if (await demuxOne(p, "audio")) did = true;
      }
    } catch (e) {
      if (!p.abort) fail(p, e && e.message);
      break;
    }
    if (did) continue;
    await sleep(IDLE_SLEEP_MS);
  }
  loopOn = false;
}

function fail(p, msg) {
  self.postMessage({ type: "fail", id: p.id, message: String(msg || "demux failed") });
}

function bindPort(next) {
  port = next;
  if (!port) return;
  port.onmessage = (ev) => {
    const msg = ev.data || {};
    if (msg.type === "credit") {
      vCredit = Number(msg.video) || 0;
      aCredit = Number(msg.audio) || 0;
    }
  };
}

function applyPriority(msg) {
  listenId = msg.listen || null;
  warmIds = Array.isArray(msg.warms) ? msg.warms.filter(Boolean) : (msg.warm ? [msg.warm] : []);
  warmId = warmIds[0] || null;
  prevId = msg.prev || null;
  if (pipe) promoteListen(pipe);
}

function reviveEos(p, kind) {
  const eosKey = kind === "video" ? "videoEos" : "audioEos";
  if (!p[eosKey]) return 0;
  p[eosKey] = false;
  const last = kind === "video" ? (p.vLastEnd || 0) : (p.aLastEnd || 0);
  if (kind === "video") p.viter = null;
  else p.aiter = null;
  if (last >= 0.08) {
    if (kind === "video") {
      p.vBase = (p.vBase || 0) + last;
      p.vLastEnd = 0;
      p.videoLoop += 1;
    } else {
      p.aBase = (p.aBase || 0) + last;
      p.aLastEnd = 0;
      p.audioLoop += 1;
    }
  }
  p.startPts = 0;
  return 1;
}

function promoteListen(p) {
  if (!p || p.abort || p.id !== listenId) return;
  p._idleEosLog = false;
  const n = reviveEos(p, "video") + reviveEos(p, "audio");
  if (n) {
    self.postMessage({
      type: "log",
      id: p.id,
      tag: "listen-wrap",
      message: "v=" + p.videoLoop + " a=" + p.audioLoop,
    });
  }
}

function rewindPipe(p, epoch) {
  p.epoch = Number(epoch) || 0;
  p.vBase = 0;
  p.aBase = 0;
  p.vLastEnd = 0;
  p.aLastEnd = 0;
  p.videoLoop = 0;
  p.audioLoop = 0;
  p.startPts = 0;
  p.viter = null;
  p.aiter = null;
  p.videoEos = false;
  p.audioEos = false;
  p._idleEosLog = false;
  vCredit = 8;
  aCredit = 8;
  self.postMessage({ type: "log", id: p.id, tag: "rewind", message: "epoch=" + p.epoch });
}

function closePipe(p) {
  if (!p) return;
  p.abort = true;
  loopOn = false;
  p.viter = null;
  p.aiter = null;
  p.input = null;
  try { port && port.close && port.close(); } catch (e) {}
  port = null;
  self.postMessage({ type: "closed", id: p.id, role: "demux" });
}

async function startPipe(p) {
  const mediaUrl = await pickMediaPlaylist(p.url);
  p.input = new Input({
    source: new UrlSource(mediaUrl, { requestInit: mediaInit() }),
    formats: HLS_FORMATS,
  });
  const vtrack = await p.input.getPrimaryVideoTrack();
  if (!vtrack) throw new Error("no video track");
  const vcfg = await vtrack.getDecoderConfig();
  if (codecKind(vcfg && vcfg.codec) !== "avc") {
    throw new Error("first version only supports H.264");
  }
  let vcfgUse = Object.assign({}, vcfg, { optimizeForLatency: true });
  if (port) {
    const cfg = cloneConfig(vcfgUse);
    const xfer = cfg.description ? [cfg.description] : [];
    port.postMessage({ type: "vconfig", epoch: p.epoch, config: cfg }, xfer);
  }
  if (p.dropAudio) {
    self.postMessage({ type: "noaudio", id: p.id, reason: "drop-audio" });
    if (port) port.postMessage({ type: "noaudio", epoch: p.epoch });
    aCredit = 0;
  } else {
  const atrack = await p.input.getPrimaryAudioTrack();
  if (!atrack) {
    self.postMessage({ type: "noaudio", id: p.id });
    if (port) port.postMessage({ type: "noaudio", epoch: p.epoch });
    aCredit = 0;
  } else {
    try {
      const acfg = await atrack.getDecoderConfig();
      if (port) {
        const cfg = cloneConfig(acfg);
        const xfer = cfg.description ? [cfg.description] : [];
        port.postMessage({ type: "aconfig", epoch: p.epoch, config: cfg }, xfer);
      }
      p.asink = new EncodedPacketSink(atrack);
    } catch (e) {
      self.postMessage({ type: "noaudio", id: p.id, reason: String((e && e.message) || e) });
      if (port) port.postMessage({ type: "noaudio", epoch: p.epoch });
      aCredit = 0;
    }
  }
  }
  p.vsink = new EncodedPacketSink(vtrack);
  self.postMessage({ type: "opened", id: p.id });
  runDemux();
}

self.onmessage = async (ev) => {
  const msg = ev.data || {};
  if (msg.type === "open") {
    if (msg.token) authToken = msg.token;
    applyPriority(msg);
    if (msg.port) bindPort(msg.port);
    if (pipe) closePipe(pipe);
    const p = {
      id: msg.id,
      url: msg.url,
      abort: false,
      videoLoop: 0,
      audioLoop: 0,
      viter: null,
      aiter: null,
      videoEos: false,
      audioEos: false,
      vBase: 0,
      aBase: 0,
      vLastEnd: 0,
      aLastEnd: 0,
      epoch: Number(msg.epoch) || 0,
      seq: Number(msg.seq),
      startPts: Number(msg.startPts) || 0,
      dropAudio: !!msg.dropAudio,
    };
    if (!Number.isFinite(p.seq)) p.seq = -1;
    pipe = p;
    try {
      await startPipe(p);
    } catch (e) {
      fail(p, e.message);
      closePipe(p);
      pipe = null;
    }
    return;
  }
  if (msg.type === "rewind") {
    if (!pipe || pipe.abort) return;
    rewindPipe(pipe, msg.epoch);
    return;
  }
  if (msg.type === "listen") {
    listenId = msg.id || null;
    if (pipe) promoteListen(pipe);
    return;
  }
  if (msg.type === "priority") {
    applyPriority(msg);
    return;
  }
  if (msg.type === "close") {
    if (pipe) {
      closePipe(pipe);
      pipe = null;
    }
    return;
  }
};
