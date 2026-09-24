const SNAP_SEC = 1;
const SYNC_TIGHT = 0.2;
const STUCK_MS = 2000;
const FIRST_FRAME_MS = 8000;
const HOLE_THRESH = 0.2;
const HELD_EVERY_MS = 50;
const STATUS_EVERY_MS = 100;
const DRAW_EVERY_MS = 1000;
const DUR_MIN = 1 / 120;
const DUR_MAX = 0.1;
const DUR_FALLBACK = 1 / 30;
const PIPE_ACCEL_SEC = 0.2;
const PIPE_ACCEL_N = 3;

function wallSec() {
  return performance.now() / 1000;
}

function createClock() {
  // vclock: abs(vpts - vclock) > 1s → set vclock
  let diff = 0;
  let primed = false;
  return {
    set: function (pts) {
      diff = wallSec() - Number(pts);
      primed = true;
    },
    get: function () {
      return wallSec() - diff;
    },
    primed: function () {
      return primed;
    },
    clear: function () {
      primed = false;
    }
  };
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

function slog() {
  try { console.log.apply(console, ["[mt-shorts-hw]"].concat([].slice.call(arguments))); } catch (e) {}
}

export function create(opts) {
  opts = opts || {};
  const workerVer = (opts.assetVersion || (typeof import.meta !== "undefined" && import.meta.url && new URL(import.meta.url).searchParams.get("v")) || "");
  const workers = new Map();
  const pipes = new Map();
  let raf = 0;
  let heldAt = 0;
  let statusAt = 0;
  let onStatus = opts.onStatus || function () {};

  function canvasOf(st) {
    return (st && st.canvas) || null;
  }

  function draw(st, src) {
    const cv = canvasOf(st);
    if (!cv || !src) return;
    const box = cv.parentElement;
    const dpr = Math.min(2, window.devicePixelRatio || 1);
    let dw = src.displayWidth || src.width || 2;
    let dh = src.displayHeight || src.height || 2;
    if (box && box.clientWidth > 8 && box.clientHeight > 8) {
      dw = Math.max(2, Math.round(box.clientWidth * dpr));
      dh = Math.max(2, Math.round(box.clientHeight * dpr));
    }
    if (cv.width !== dw) cv.width = dw;
    if (cv.height !== dh) cv.height = dh;
    cv.getContext("2d").drawImage(src, 0, 0, dw, dh);
  }

  function closeItem(x) {
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

  function flushVideo(st) {
    if (!st || !st.video) return;
    st.video.forEach(closeItem);
    st.video = [];
  }

  function markFail(st, reason) {
    if (!st || st.closed) return;
    st.failed = true;
    st.reason = reason || "fail";
    slog("fail", st.id, st.reason);
    onStatus();
  }

  function reportHeld(id) {
    const st = pipes.get(id);
    const pair = workers.get(id);
    if (!st || !pair) return;
    try {
      pair.decode.postMessage({ type: "held", id: id, video: dequeRunSeconds(st.video), audio: 0 });
    } catch (e) {}
  }

  function noteFps(st) {
    const t = performance.now();
    st.fpsAt = st.fpsAt || [];
    st.fpsAt.push(t);
    const cut = t - 1000;
    while (st.fpsAt.length && st.fpsAt[0] < cut) st.fpsAt.shift();
    if (st.fpsAt.length >= 2) {
      const span = (st.fpsAt[st.fpsAt.length - 1] - st.fpsAt[0]) / 1000;
      st.fps = span > 0 ? (st.fpsAt.length - 1) / span : 0;
    } else {
      st.fps = st.fpsAt.length ? 1 : 0;
    }
  }

  function clampFrameDur(d) {
    if (!Number.isFinite(d) || d < DUR_MIN || d > DUR_MAX) return DUR_FALLBACK;
    return d;
  }

  function frameDur(item, lastPts) {
    const pts = Number(item && item.pts);
    const delta = pts - Number(lastPts);
    if (Number.isFinite(delta) && delta >= DUR_MIN && delta <= DUR_MAX) return delta;
    return clampFrameDur(item && item.duration);
  }

  function pipeDeep(st) {
    return !!(st && (st.video.length >= PIPE_ACCEL_N || dequeRunSeconds(st.video) >= PIPE_ACCEL_SEC));
  }

  function dropHead(st) {
    closeItem(st.video.shift());
  }

  function paint(st, vis) {
    if (!vis) return;
    const t = performance.now();
    const drawIt = !st.lastDraw || t - st.lastDraw >= DRAW_EVERY_MS;
    if (drawIt) {
      if (vis.bmp) draw(st, vis.bmp);
      else if (vis.frame) draw(st, vis.frame);
      st.lastDraw = t;
    }
    if (vis.bmp) {
      try { vis.bmp.close(); } catch (e) {}
      vis.bmp = null;
    }
    if (vis.frame) {
      try { vis.frame.close(); } catch (e) {}
      vis.frame = null;
    }
    st.lastPts = Number(vis.pts);
    st.lastPresent = t;
    st.gotFrame = true;
    if (st.firstTimer) {
      clearTimeout(st.firstTimer);
      st.firstTimer = 0;
    }
  }

  function presentOne(st) {
    if (!st || st.failed || st.closed) return;
    let lastPts = st.lastPts;
    while (st.video.length) {
      const p0 = Number(st.video[0].pts);
      if (!Number.isFinite(p0)) {
        dropHead(st);
        continue;
      }
      if (Number.isFinite(lastPts) && p0 < lastPts - 0.001) {
        dropHead(st);
        continue;
      }
      break;
    }
    if (!st.video.length) return;
    const head = st.video[0];
    const pts = Number(head.pts);
    const dur = frameDur(head, lastPts);
    if (!st.clock.primed()) {
      st.clock.set(pts);
      st.lastClock = null;
      paint(st, st.video.shift());
      return;
    }
    const now = st.clock.get();
    if (Math.abs(pts - now) > SNAP_SEC) {
      st.clock.set(pts);
      st.lastClock = null;
      paint(st, st.video.shift());
      return;
    }
    if (Math.abs(pts - now) <= SYNC_TIGHT) {
      st.lastClock = null;
      if (pts >= now) return;
      let due = null;
      while (st.video.length) {
        const p = Number(st.video[0].pts);
        if (!Number.isFinite(p)) {
          dropHead(st);
          continue;
        }
        if (Number.isFinite(lastPts) && p < lastPts - 0.001) {
          dropHead(st);
          continue;
        }
        if (p >= now) break;
        if (due) closeItem(due);
        due = st.video.shift();
        lastPts = p;
      }
      if (due) paint(st, due);
      return;
    }
    if (pts < now) {
      const rate = pipeDeep(st) ? 2 : 1;
      if (st.lastClock == null || now - st.lastClock > dur / rate) {
        st.lastClock = now;
        paint(st, st.video.shift());
      }
      return;
    }
    if (st.lastClock == null || now - st.lastClock > 2 * dur) {
      st.lastClock = now;
      paint(st, st.video.shift());
    }
  }

  function tick() {
    raf = requestAnimationFrame(tick);
    const now = performance.now();
    pipes.forEach(function (st) {
      if (st.closed) return;
      presentOne(st);
    });
    if (now - heldAt >= HELD_EVERY_MS) {
      heldAt = now;
      pipes.forEach(function (st) {
        if (!st.closed && !st.failed) reportHeld(st.id);
      });
    }
    if (now - statusAt >= STATUS_EVERY_MS) {
      statusAt = now;
      onStatus();
    }
  }

  function onWorkerMessage(ev) {
    const msg = ev.data || {};
    const st = pipes.get(msg.id);
    if (msg.type === "fail") {
      if (st) markFail(st, msg.message || "decode failed");
      return;
    }
    if (msg.type === "decoder") {
      slog("decoder", msg.action, msg.kind, msg.id, msg.message || "");
      if (st && msg.kind === "video" && msg.action === "close" && !st.closed && !st.failed) {
        markFail(st, "decoder-closed");
      }
      return;
    }
    if (msg.type === "closed") {
      if (st && !st.closed && !st.failed) markFail(st, "pipe-closed");
      return;
    }
    if (!st) {
      if (msg.frame) { try { msg.frame.close(); } catch (e) {} }
      if (msg.bmp) { try { msg.bmp.close(); } catch (e) {} }
      return;
    }
    if (msg.type === "opened") {
      st.openedAt = performance.now();
      st.firstTimer = setTimeout(function () {
        if (st && !st.gotFrame && !st.failed && !st.closed) markFail(st, "首帧超时");
      }, FIRST_FRAME_MS);
      return;
    }
    if (msg.type === "video") {
      if (st.failed || st.closed) {
        if (msg.frame) { try { msg.frame.close(); } catch (e) {} }
        if (msg.bmp) { try { msg.bmp.close(); } catch (e) {} }
        return;
      }
      noteFps(st);
      st.gotFrame = true;
      if (st.firstTimer) {
        clearTimeout(st.firstTimer);
        st.firstTimer = 0;
      }
      st.video.push({
        frame: msg.frame || null,
        bmp: msg.bmp || null,
        pts: Number(msg.pts),
        duration: Number(msg.duration) || DUR_FALLBACK
      });
      return;
    }
    if (msg.type === "audio") return;
    if (msg.type === "log") slog(msg.tag || "worker", msg.id, msg.message || "");
  }

  function open(id, url, canvas) {
    close(id);
    const st = {
      id: id,
      canvas: canvas || null,
      video: [],
      clock: createClock(),
      failed: false,
      closed: false,
      gotFrame: false,
      firstTimer: 0,
      lastPresent: 0,
      lastPts: NaN,
      lastClock: null,
      lastDraw: 0,
      fpsAt: [],
      fps: 0,
      reason: ""
    };
    pipes.set(id, st);
    const tok = (opts.getToken && opts.getToken()) || opts.token || "";
    const demux = new Worker("/static/shorts-worker.js?v=" + workerVer, { type: "module" });
    const decode = new Worker("/static/shorts-decode-worker.js?v=" + workerVer);
    const ch = new MessageChannel();
    demux.onmessage = onWorkerMessage;
    decode.onmessage = onWorkerMessage;
    workers.set(id, { demux: demux, decode: decode });
    decode.postMessage({
      type: "open",
      id: id,
      seq: 0,
      epoch: 1,
      port: ch.port2,
      listen: id,
      warm: null,
      prev: null,
      dropAudio: true,
      rawFrames: true
    }, [ch.port2]);
    demux.postMessage({
      type: "open",
      id: id,
      url: url,
      startPts: 0,
      token: tok,
      seq: 0,
      epoch: 1,
      port: ch.port1,
      listen: id,
      warm: null,
      prev: null,
      dropAudio: true
    }, [ch.port1]);
    slog("open", id);
    if (!raf) raf = requestAnimationFrame(tick);
    onStatus();
    return st;
  }

  function close(id) {
    const st = pipes.get(id);
    if (st) {
      st.closed = true;
      if (st.firstTimer) clearTimeout(st.firstTimer);
      flushVideo(st);
      st.clock.clear();
    }
    const cv = st && st.canvas;
    pipes.delete(id);
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
    if (cv) {
      const ctx = cv.getContext("2d");
      ctx.clearRect(0, 0, cv.width, cv.height);
    }
    onStatus();
  }

  function closeAll() {
    Array.from(pipes.keys()).forEach(close);
    if (raf) cancelAnimationFrame(raf);
    raf = 0;
    onStatus();
  }

  function healthy() {
    let n = 0;
    pipes.forEach(function (st) {
      if (!st.closed && !st.failed && st.gotFrame && performance.now() - st.lastPresent <= STUCK_MS) n += 1;
    });
    return n;
  }

  function stateOf(id) {
    const st = pipes.get(id);
    if (!st) return "idle";
    if (st.failed || st.closed) return "bad";
    if (st.gotFrame && performance.now() - st.lastPresent <= STUCK_MS) return "ok";
    if (st.gotFrame) return "bad";
    return "pending";
  }

  function fpsOf(id) {
    const st = pipes.get(id);
    if (!st || st.failed || st.closed) return 0;
    return Number(st.fps) || 0;
  }

  return { open, close, closeAll, healthy, stateOf, fpsOf, pipes };
}
