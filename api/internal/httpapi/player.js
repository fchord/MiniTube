(function (w) {
  const SPEEDS = [0.5, 0.75, 1, 1.25, 1.5, 2, 2.5, 3];
  const ASPECTS = [
    { id: "original", label: "原始比例" },
    { id: "4:3", label: "4:3", ratio: 4 / 3 },
    { id: "16:9", label: "16:9", ratio: 16 / 9 },
    { id: "16:10", label: "16:10", ratio: 16 / 10 },
    { id: "1.85:1", label: "1.85:1", ratio: 1.85 },
    { id: "2.35:1", label: "2.35:1", ratio: 2.35 }
  ];
  const PREF_KEY = "mt.player.prefs";
  const ICONS = {
    play: '<svg viewBox="0 0 24 24"><path d="M8 5v14l11-7z"/></svg>',
    pause: '<svg viewBox="0 0 24 24"><path d="M6 5h4v14H6zm8 0h4v14h-4z"/></svg>',
    vol: '<svg viewBox="0 0 24 24"><path d="M5 10v4h4l5 4V6l-5 4H5zm11.5 2a3.5 3.5 0 0 0-1.8-3.1v6.2A3.5 3.5 0 0 0 16.5 12zm-1.8-7.4v2.1A6.5 6.5 0 0 1 19.5 12a6.5 6.5 0 0 1-4.8 6.3v2.1A8.5 8.5 0 0 0 21.5 12a8.5 8.5 0 0 0-6.8-8.4z"/></svg>',
    mute: '<svg viewBox="0 0 24 24"><path d="M5 10v4h4l5 4V6l-5 4H5zm12.5 1.5 2-2 1.4 1.4-2 2 2 2-1.4 1.4-2-2-2 2-1.4-1.4 2-2-2-2 1.4-1.4 2 2z"/></svg>',
    fs: '<svg viewBox="0 0 24 24"><path d="M4 4h6v2H6v4H4V4zm10 0h6v6h-2V6h-4V4zM4 14h2v4h4v2H4v-6zm14 0h2v6h-6v-2h4v-4z"/></svg>',
    fsExit: '<svg viewBox="0 0 24 24"><path d="M8 4H4v4h2V6h2V4zm12 0h-4v2h2v2h2V4zM6 16H4v4h4v-2H6v-2zm14 0h-2v2h-2v2h4v-4z"/></svg>',
    more: '<svg viewBox="0 0 24 24"><path d="M6 10a2 2 0 1 1 0 4 2 2 0 0 1 0-4zm6 0a2 2 0 1 1 0 4 2 2 0 0 1 0-4zm6 0a2 2 0 1 1 0 4 2 2 0 0 1 0-4z"/></svg>'
  };

  function loadPrefs() {
    try { return Object.assign({}, JSON.parse(localStorage.getItem(PREF_KEY) || "{}")); }
    catch (e) { return {}; }
  }
  function savePrefs(p) {
    try { localStorage.setItem(PREF_KEY, JSON.stringify(p)); } catch (e) {}
  }
  function fmtTime(s) {
    if (!isFinite(s) || s < 0) return "0:00";
    s = Math.floor(s);
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    const sec = String(s % 60).padStart(2, "0");
    if (h) return h + ":" + String(m).padStart(2, "0") + ":" + sec;
    return m + ":" + sec;
  }
  function fmtSpeed(r) {
    const n = Number(r);
    if (n === 1) return "1x";
    return String(n) + "x";
  }
  function typingTarget(el) {
    if (!el) return false;
    const tag = (el.tagName || "").toLowerCase();
    if (tag === "input" || tag === "textarea" || tag === "select") return true;
    return !!el.isContentEditable;
  }
  function canHlsJs() { return !!(w.Hls && Hls.isSupported()); }
  function isCoarseMobile() {
    try {
      if (!(w.matchMedia && w.matchMedia("(pointer: coarse)").matches)) return false;
      return Math.min(w.innerWidth || 0, w.innerHeight || 0) <= 920;
    } catch (e) {
      return false;
    }
  }
  function fsEl() {
    return document.fullscreenElement || document.webkitFullscreenElement || null;
  }
  function sameOriginMedia(url) {
    if (w.mtMedia && w.mtMedia.rewrite) return w.mtMedia.rewrite(url);
    if (!url) return url;
    try {
      const u = new URL(url, w.location.origin);
      const p = u.pathname || "";
      if (p.indexOf("/v1/media/") === 0 || p.indexOf("/v1/live/") === 0) {
        return w.location.origin + p + u.search + u.hash;
      }
    } catch (e) {}
    return url;
  }
  function hlsOpts(extra) {
    extra = extra || {};
    return w.mtAuth && w.mtAuth.hlsConfig ? w.mtAuth.hlsConfig(extra) : extra;
  }
  function streamingOpts(live) {
    const extra = {
      enableWorker: true,
      maxBufferLength: 10,
      maxMaxBufferLength: 18,
      maxBufferSize: 30 * 1000 * 1000,
      backBufferLength: 30,
      capLevelToPlayerSize: true,
      startLevel: 0,
      abrEwmaDefaultEstimate: 500000,
      progressive: false
    };
    if (live) {
      extra.lowLatencyMode = false;
      extra.liveSyncDurationCount = 3;
      extra.maxLiveSyncPlaybackRate = 1;
      extra.liveDurationInfinity = true;
      extra.backBufferLength = 45;
      extra.manifestLoadingTimeOut = 20000;
      extra.levelLoadingTimeOut = 20000;
      extra.fragLoadingTimeOut = 20000;
    }
    return extra;
  }

  function mount(root) {
    if (!root) throw new Error("player root missing");
    root.className = "mt-player is-paused";
    root.innerHTML =
      '<div class="mt-stage">' +
        '<video playsinline></video>' +
      "</div>" +
      '<p class="mt-error" data-err></p>' +
      '<div class="mt-hud" data-hud hidden></div>' +
      '<div class="mt-gestures" data-gestures></div>' +
      '<div class="mt-chrome">' +
        '<button type="button" class="mt-bigplay" data-bigplay aria-label="播放">' + ICONS.play + "</button>" +
        '<div class="mt-controls">' +
          '<div class="mt-progress">' +
            '<div class="mt-progress-track">' +
              '<div class="mt-progress-buf" data-buf></div>' +
              '<div class="mt-progress-play" data-play></div>' +
            "</div>" +
            '<input type="range" min="0" max="1000" value="0" step="1" aria-label="进度"/>' +
          "</div>" +
          '<div class="mt-bar">' +
            '<button type="button" class="mt-btn" data-playbtn aria-label="播放">' + ICONS.play + "</button>" +
            '<span class="mt-time" data-time>0:00 / 0:00</span>' +
            '<span class="spacer"></span>' +
            '<div class="mt-speed-wrap">' +
              '<button type="button" class="mt-btn mt-speed-btn" data-speedbtn aria-haspopup="true" aria-expanded="false">1x</button>' +
              '<div class="mt-menu mt-speed-menu" data-speedmenu hidden></div>' +
            "</div>" +
            '<div class="mt-vol">' +
              '<button type="button" class="mt-btn" data-mute aria-label="静音">' + ICONS.vol + "</button>" +
              '<input type="range" min="0" max="100" value="100" aria-label="音量"/>' +
            "</div>" +
            '<button type="button" class="mt-btn" data-fs aria-label="全屏">' + ICONS.fs + "</button>" +
            '<div class="mt-more-wrap">' +
              '<button type="button" class="mt-btn" data-morebtn aria-label="更多" aria-haspopup="true" aria-expanded="false">' + ICONS.more + "</button>" +
              '<div class="mt-menu" data-moremenu hidden></div>' +
            "</div>" +
          "</div>" +
        "</div>" +
      "</div>";

    const video = root.querySelector("video");
    const stage = root.querySelector(".mt-stage");
    const errEl = root.querySelector("[data-err]");
    const chrome = root.querySelector(".mt-chrome");
    const gestures = root.querySelector("[data-gestures]");
    const bigPlay = root.querySelector("[data-bigplay]");
    const hud = root.querySelector("[data-hud]");
    const playBtn = root.querySelector("[data-playbtn]");
    const timeEl = root.querySelector("[data-time]");
    const bufEl = root.querySelector("[data-buf]");
    const playEl = root.querySelector("[data-play]");
    const seek = root.querySelector(".mt-progress input");
    const speedBtn = root.querySelector("[data-speedbtn]");
    const speedMenu = root.querySelector("[data-speedmenu]");
    const muteBtn = root.querySelector("[data-mute]");
    const vol = root.querySelector(".mt-vol input");
    const fsBtn = root.querySelector("[data-fs]");
    const moreBtn = root.querySelector("[data-morebtn]");
    const moreMenu = root.querySelector("[data-moremenu]");

    const prefs = Object.assign({
      speed: 1,
      quality: "auto",
      compressor: false,
      mirror: false,
      aspect: "original"
    }, loadPrefs());
    if (SPEEDS.indexOf(Number(prefs.speed)) < 0) prefs.speed = 1;

    let hls = null;
    let renditions = [];
    let metaW = 0;
    let metaH = 0;
    let autoHeight = 0;
    let idleTimer = 0;
    let liveTick = 0;
    let liveMode = false;
    let attachedUrl = "";
    let startAt = 0;
    let wantPlay = false;
    let onTimeupdateCb = null;
    let onHlsError = null;
    let scrubTime = null;
    let quietIdle = false;
    let recentTouch = 0;
    let tapTimer = 0;
    let lastTapAt = 0;
    let lastTapX = 0;
    let lastTapY = 0;
    let ptr = null;
    let audioCtx = null;
    let mediaSrc = null;
    let dryGain = null;
    let wetGain = null;
    const listeners = [];

    function on(el, ev, fn, opts) {
      el.addEventListener(ev, fn, opts);
      listeners.push(function () { el.removeEventListener(ev, fn, opts); });
    }
    function setErr(msg) { errEl.textContent = msg || ""; }
    function persist() { savePrefs(prefs); }
    function menuOpen() {
      return !speedMenu.hidden || !moreMenu.hidden;
    }
    function closeMenus() {
      speedMenu.hidden = true;
      moreMenu.hidden = true;
      speedBtn.setAttribute("aria-expanded", "false");
      moreBtn.setAttribute("aria-expanded", "false");
      root.classList.remove("is-menu");
    }
    function pingIdle() {
      root.classList.remove("is-idle");
      clearTimeout(idleTimer);
      if (menuOpen()) return;
      idleTimer = setTimeout(function () { root.classList.add("is-idle"); }, 2500);
    }
    function forceIdle() {
      clearTimeout(idleTimer);
      root.classList.add("is-idle");
    }
    function chromeVisible() {
      return !root.classList.contains("is-idle") || menuOpen();
    }
    function sourceRatio() {
      if (video.videoWidth && video.videoHeight) return video.videoWidth / video.videoHeight;
      if (metaW && metaH) return metaW / metaH;
      return 16 / 9;
    }
    function applyAspect() {
      const spec = ASPECTS.find(function (a) { return a.id === prefs.aspect; }) || ASPECTS[0];
      const ar = spec.ratio || sourceRatio();
      root.style.setProperty("--mt-ar", String(ar));
      stage.style.setProperty("--mt-ar", String(ar));
    }
    function applyMirror() {
      root.classList.toggle("is-mirror", !!prefs.mirror);
    }
    function applySpeed() {
      video.playbackRate = prefs.speed;
      speedBtn.textContent = fmtSpeed(prefs.speed);
    }
    function applyQuality() {
      if (!hls) return;
      if (prefs.quality === "auto") {
        hls.capLevelToPlayerSize = true;
        hls.currentLevel = -1;
        return;
      }
      const h = Number(prefs.quality);
      const idx = hls.levels.findIndex(function (l) { return l.height === h; });
      if (idx >= 0) {
        hls.capLevelToPlayerSize = false;
        hls.currentLevel = idx;
      } else {
        hls.capLevelToPlayerSize = true;
        hls.currentLevel = -1;
      }
    }
    function canManualQuality() { return !!hls; }

    function ensureAudio() {
      if (mediaSrc) {
        if (audioCtx && audioCtx.state === "suspended") audioCtx.resume().catch(function () {});
        return true;
      }
      const AC = w.AudioContext || w.webkitAudioContext;
      if (!AC) return false;
      try {
        audioCtx = new AC();
        mediaSrc = audioCtx.createMediaElementSource(video);
        const comp = audioCtx.createDynamicsCompressor();
        comp.threshold.value = -24;
        comp.knee.value = 30;
        comp.ratio.value = 12;
        comp.attack.value = 0.003;
        comp.release.value = 0.25;
        const makeup = audioCtx.createGain();
        makeup.gain.value = 1.2;
        dryGain = audioCtx.createGain();
        wetGain = audioCtx.createGain();
        mediaSrc.connect(dryGain);
        mediaSrc.connect(comp);
        comp.connect(makeup);
        makeup.connect(wetGain);
        dryGain.connect(audioCtx.destination);
        wetGain.connect(audioCtx.destination);
        audioCtx.resume().catch(function () {});
        applyCompressor();
        return true;
      } catch (e) {
        mediaSrc = null;
        return false;
      }
    }
    function applyCompressor() {
      if (!dryGain) return;
      dryGain.gain.value = prefs.compressor ? 0 : 1;
      wetGain.gain.value = prefs.compressor ? 1 : 0;
    }

    function heights() {
      const set = {};
      renditions.forEach(function (r) {
        if (r && r.height) set[r.height] = true;
      });
      if (hls) {
        hls.levels.forEach(function (l) {
          if (l.height) set[l.height] = true;
        });
      }
      return Object.keys(set).map(Number).sort(function (a, b) { return b - a; });
    }
    function renderSpeedMenu() {
      speedMenu.innerHTML = SPEEDS.map(function (s) {
        const on = Number(prefs.speed) === s ? " is-on" : "";
        return '<button type="button" class="' + on.trim() + '" data-speed="' + s + '">' + fmtSpeed(s) + "</button>";
      }).join("");
    }
    function renderMoreMenu() {
      const hs = heights();
      const manual = canManualQuality();
      let q = '<div class="mt-menu-label">清晰度</div>';
      const autoOn = prefs.quality === "auto" || !manual || hs.indexOf(Number(prefs.quality)) < 0;
      const autoLabel = autoHeight && autoOn ? "自动 (" + autoHeight + "p)" : "自动";
      q += '<button type="button" data-q="auto" class="' + (autoOn ? "is-on" : "") + '">' + autoLabel + "</button>";
      hs.forEach(function (h) {
        const on = manual && Number(prefs.quality) === h ? " is-on" : "";
        const dis = manual ? "" : " disabled";
        q += '<button type="button" data-q="' + h + '" class="' + on.trim() + '"' + dis + ">" + h + "p</button>";
      });
      let a = '<div class="mt-menu-label mt-menu-sep">显示比例</div>';
      ASPECTS.forEach(function (spec) {
        const on = prefs.aspect === spec.id ? " is-on" : "";
        a += '<button type="button" data-ar="' + spec.id + '" class="' + on.trim() + '">' + spec.label + "</button>";
      });
      const mir = prefs.mirror ? " is-on" : "";
      const cmp = prefs.compressor ? " is-on" : "";
      moreMenu.innerHTML = q + a +
        '<div class="mt-menu-label mt-menu-sep">画面与音频</div>' +
        '<div class="mt-menu-row">左右镜像<button type="button" class="mt-toggle' + mir + '" data-toggle="mirror" aria-pressed="' + !!prefs.mirror + '"></button></div>' +
        '<div class="mt-menu-row">音频动态压缩<button type="button" class="mt-toggle' + cmp + '" data-toggle="compressor" aria-pressed="' + !!prefs.compressor + '"></button></div>';
    }
    function refreshMenus() {
      renderSpeedMenu();
      renderMoreMenu();
    }

    function setPlayingUi() {
      const paused = video.paused;
      root.classList.toggle("is-paused", paused);
      const icon = paused ? ICONS.play : ICONS.pause;
      const label = paused ? "播放" : "暂停";
      playBtn.innerHTML = icon;
      playBtn.setAttribute("aria-label", label);
      bigPlay.innerHTML = icon;
      bigPlay.setAttribute("aria-label", label);
      if (quietIdle) {
        quietIdle = false;
        forceIdle();
        return;
      }
      pingIdle();
    }
    function setMuteUi() {
      muteBtn.innerHTML = video.muted || video.volume === 0 ? ICONS.mute : ICONS.vol;
      muteBtn.setAttribute("aria-label", video.muted ? "取消静音" : "静音");
    }
    function setFsUi() {
      const on = !!fsEl();
      fsBtn.innerHTML = on ? ICONS.fsExit : ICONS.fs;
      fsBtn.setAttribute("aria-label", on ? "退出全屏" : "全屏");
    }
    function unlockOrientation() {
      try {
        if (w.screen && screen.orientation && screen.orientation.unlock) screen.orientation.unlock();
      } catch (e) {}
    }
    function lockLandscape() {
      if (!isCoarseMobile()) return;
      try {
        if (!w.screen || !screen.orientation || !screen.orientation.lock) return;
        const p = screen.orientation.lock("landscape");
        if (p && p.catch) p.catch(function () {});
      } catch (e) {}
    }
    function portraitViewport() {
      return (w.innerHeight || 0) > (w.innerWidth || 0);
    }
    function syncFsRotate() {
      const on = !!fsEl() && isCoarseMobile() && portraitViewport();
      root.classList.toggle("is-fs-rotate", on);
    }
    function onFsChange() {
      setFsUi();
      if (fsEl()) {
        lockLandscape();
        syncFsRotate();
      } else {
        root.classList.remove("is-fs-rotate");
        unlockOrientation();
      }
    }
    function seekableEnd() {
      try {
        if (video.seekable && video.seekable.length) {
          return video.seekable.end(video.seekable.length - 1);
        }
      } catch (e) {}
      return 0;
    }
    function liveEdge() {
      let edge = 0;
      if (hls && typeof hls.liveSyncPosition === "number" && isFinite(hls.liveSyncPosition)) {
        edge = Math.max(edge, hls.liveSyncPosition);
      }
      edge = Math.max(edge, seekableEnd());
      return edge;
    }
    function mediaDuration() {
      let end = liveEdge();
      const d = video.duration;
      if (isFinite(d) && d > 0) end = Math.max(end, d);
      return end;
    }
    function nearLiveEdge() {
      const edge = liveEdge();
      const t = scrubTime != null ? scrubTime : (video.currentTime || 0);
      if (!(edge > 0)) return false;
      return edge - t <= 1.5;
    }
    function snapSpeedIfLive() {
      if (!liveMode) return;
      if (Number(prefs.speed) <= 1) return;
      if (!nearLiveEdge()) return;
      prefs.speed = 1;
      applySpeed();
      renderSpeedMenu();
    }
    function startLiveTick() {
      stopLiveTick();
      liveTick = setInterval(function () {
        updateProgress();
        snapSpeedIfLive();
      }, 250);
    }
    function stopLiveTick() {
      if (liveTick) {
        clearInterval(liveTick);
        liveTick = 0;
      }
    }
    function paintPlayhead(t) {
      const d = mediaDuration();
      timeEl.textContent = fmtTime(t) + " / " + fmtTime(d);
      if (d > 0) {
        const pct = Math.max(0, Math.min(1, t / d));
        playEl.style.width = (pct * 100) + "%";
        seek.value = String(Math.round(pct * 1000));
      }
    }
    function updateProgress() {
      const t = scrubTime != null ? scrubTime : (video.currentTime || 0);
      paintPlayhead(t);
      const d = mediaDuration();
      if (d > 0 && video.buffered.length) {
        try {
          const end = video.buffered.end(video.buffered.length - 1);
          bufEl.style.width = Math.min(100, (end / d) * 100) + "%";
        } catch (e) {}
      }
      if (typeof onTimeupdateCb === "function") onTimeupdateCb(t);
    }
    function jumpTo(t) {
      const d = mediaDuration();
      if (!(d > 0)) return;
      scrubTime = Math.max(0, Math.min(d, t));
      paintPlayhead(scrubTime);
      video.currentTime = scrubTime;
    }
    function clearScrub() {
      if (scrubTime == null) return;
      if (video.seeking) return;
      scrubTime = null;
      updateProgress();
    }
    function togglePlay() {
      if (video.paused) video.play().catch(function () {});
      else video.pause();
    }
    function togglePlayQuiet() {
      quietIdle = true;
      togglePlay();
    }
    function clamp(n, lo, hi) {
      return Math.max(lo, Math.min(hi, n));
    }
    function showHud(text) {
      hud.hidden = false;
      hud.textContent = text;
    }
    function hideHud() {
      hud.hidden = true;
      hud.textContent = "";
    }
    function previewSeek(t) {
      const d = mediaDuration();
      if (!(d > 0)) return;
      scrubTime = clamp(t, 0, d);
      paintPlayhead(scrubTime);
    }
    function commitPreviewSeek() {
      if (scrubTime == null) return;
      jumpTo(scrubTime);
    }
    function singleTapChrome() {
      if (menuOpen()) { closeMenus(); return; }
      if (chromeVisible()) forceIdle();
      else pingIdle();
    }
    function onGestureDown(e) {
      if (e.button != null && e.button !== 0) return;
      if (ptr) return;
      ptr = {
        id: e.pointerId,
        x: e.clientX,
        y: e.clientY,
        touch: e.pointerType === "touch" || e.pointerType === "pen",
        mode: "maybe",
        startT: scrubTime != null ? scrubTime : (video.currentTime || 0),
        startVol: video.muted ? 0 : video.volume
      };
      try { gestures.setPointerCapture(e.pointerId); } catch (err) {}
    }
    function onGestureMove(e) {
      if (!ptr || e.pointerId !== ptr.id) return;
      const dx = e.clientX - ptr.x;
      const dy = e.clientY - ptr.y;
      if (ptr.mode === "maybe") {
        if (Math.abs(dx) < 14 && Math.abs(dy) < 14) return;
        if (!ptr.touch) {
          ptr.mode = "tap";
          return;
        }
        if (Math.abs(dx) >= Math.abs(dy)) {
          ptr.mode = "seek";
          pingIdle();
        } else {
          ptr.mode = "vol";
        }
      }
      if (ptr.mode === "seek") {
        const d = mediaDuration();
        if (!(d > 0)) return;
        const w = Math.max(root.getBoundingClientRect().width, 1);
        const span = d <= 180 ? d : 180;
        previewSeek(ptr.startT + (dx / w) * span);
      } else if (ptr.mode === "vol") {
        const h = Math.max(root.getBoundingClientRect().height, 1);
        const nv = clamp(ptr.startVol - dy / h, 0, 1);
        video.muted = nv === 0;
        video.volume = nv;
        vol.value = String(Math.round(nv * 100));
        setMuteUi();
        showHud("音量 " + Math.round(nv * 100) + "%");
      }
    }
    function onGestureUp(e) {
      if (!ptr || e.pointerId !== ptr.id) return;
      const mode = ptr.mode;
      const touch = ptr.touch;
      const x = e.clientX;
      const y = e.clientY;
      ptr = null;
      hideHud();
      if (touch) recentTouch = Date.now();
      if (mode === "seek") {
        commitPreviewSeek();
        pingIdle();
        return;
      }
      if (mode === "vol") {
        pingIdle();
        return;
      }
      if (mode === "tap") return;
      const now = Date.now();
      if (touch && now - lastTapAt < 300 && Math.hypot(x - lastTapX, y - lastTapY) < 48) {
        clearTimeout(tapTimer);
        tapTimer = 0;
        lastTapAt = 0;
        closeMenus();
        togglePlayQuiet();
        forceIdle();
        return;
      }
      lastTapAt = now;
      lastTapX = x;
      lastTapY = y;
      if (touch) {
        clearTimeout(tapTimer);
        tapTimer = setTimeout(function () {
          tapTimer = 0;
          lastTapAt = 0;
          singleTapChrome();
        }, 300);
        return;
      }
      singleTapChrome();
    }
    function onGestureCancel(e) {
      if (!ptr || e.pointerId !== ptr.id) return;
      const mode = ptr.mode;
      ptr = null;
      hideHud();
      if (mode === "seek") {
        scrubTime = null;
        updateProgress();
      }
    }
    function toggleMute() {
      video.muted = !video.muted;
      if (!video.muted && video.volume === 0) video.volume = 0.5;
      vol.value = video.muted ? "0" : String(Math.round(video.volume * 100));
      setMuteUi();
    }
    function toggleFs() {
      if (fsEl()) {
        const ex = document.exitFullscreen || document.webkitExitFullscreen;
        if (ex) Promise.resolve(ex.call(document)).catch(function () {});
        return;
      }
      const req = root.requestFullscreen || root.webkitRequestFullscreen;
      if (!req) return;
      lockLandscape();
      Promise.resolve(req.call(root)).then(function () {
        lockLandscape();
        syncFsRotate();
      }).catch(function () {
        root.classList.remove("is-fs-rotate");
      });
    }
    function seekBy(delta) {
      if (!(mediaDuration() > 0)) return;
      const from = scrubTime != null ? scrubTime : (video.currentTime || 0);
      jumpTo(from + delta);
    }
    function seekPct(p) {
      const d = mediaDuration();
      if (!(d > 0)) return;
      jumpTo(d * p);
    }
    function applyStartAt() {
      if (!(startAt > 1)) return;
      const d = mediaDuration();
      if (d > 0 && Math.abs(video.currentTime - startAt) > 1.5) {
        video.currentTime = Math.min(startAt, d);
      }
      startAt = 0;
    }

    function attach(src) {
      src = sameOriginMedia(src);
      if (!src) return;
      if (hls && attachedUrl === src && liveMode) {
        hls.startLoad();
        return;
      }
      if (hls) { hls.destroy(); hls = null; }
      video.removeAttribute("src");
      video.load();
      attachedUrl = src;
      if (canHlsJs()) {
        const extra = streamingOpts(liveMode);
        if (!liveMode && src.indexOf("/v1/live/") >= 0) {
          extra.manifestLoadingTimeOut = 30000;
          extra.maxBufferLength = 8;
          extra.maxMaxBufferLength = 12;
        }
        hls = new Hls(hlsOpts(extra));
        hls.on(Hls.Events.MANIFEST_PARSED, function () {
          applyQuality();
          applyStartAt();
          refreshMenus();
          updateProgress();
          if (wantPlay) video.play().catch(function () {});
        });
        hls.on(Hls.Events.LEVEL_SWITCHED, function (_, data) {
          const lvl = hls.levels[data.level];
          autoHeight = lvl && lvl.height ? lvl.height : 0;
          if (!moreMenu.hidden) renderMoreMenu();
        });
        hls.on(Hls.Events.ERROR, function (_, data) {
          if (typeof onHlsError === "function" && onHlsError(data)) return;
          if (!data || !data.fatal) return;
          if (data.type === Hls.ErrorTypes.NETWORK_ERROR) hls.startLoad();
          else if (data.type === Hls.ErrorTypes.MEDIA_ERROR) hls.recoverMediaError();
          else setErr("播放失败");
        });
        hls.loadSource(src);
        hls.attachMedia(video);
        return;
      }
      if (video.canPlayType("application/vnd.apple.mpegurl")) {
        video.src = src;
        return;
      }
      throw new Error("当前浏览器不支持 HLS");
    }

    renderSpeedMenu();
    renderMoreMenu();
    applyMirror();
    applyAspect();
    applySpeed();
    vol.value = "100";

    on(playBtn, "click", function (e) {
      e.stopPropagation();
      closeMenus();
      togglePlay();
    });
    on(bigPlay, "click", function (e) {
      e.stopPropagation();
      closeMenus();
      togglePlay();
    });
    on(gestures, "pointerdown", onGestureDown);
    on(gestures, "pointermove", onGestureMove);
    on(gestures, "pointerup", onGestureUp);
    on(gestures, "pointercancel", onGestureCancel);
    on(gestures, "click", function (e) { e.preventDefault(); });
    on(speedBtn, "click", function (e) {
      e.stopPropagation();
      const open = speedMenu.hidden;
      closeMenus();
      if (open) {
        renderSpeedMenu();
        speedMenu.hidden = false;
        speedBtn.setAttribute("aria-expanded", "true");
        root.classList.add("is-menu");
      }
      pingIdle();
    });
    on(speedMenu, "click", function (e) {
      const btn = e.target.closest("[data-speed]");
      if (!btn) return;
      prefs.speed = Number(btn.getAttribute("data-speed"));
      persist();
      applySpeed();
      closeMenus();
      pingIdle();
    });
    on(moreBtn, "click", function (e) {
      e.stopPropagation();
      const open = moreMenu.hidden;
      closeMenus();
      if (open) {
        renderMoreMenu();
        moreMenu.hidden = false;
        moreBtn.setAttribute("aria-expanded", "true");
        root.classList.add("is-menu");
      }
      pingIdle();
    });
    on(moreMenu, "click", function (e) {
      e.stopPropagation();
      const q = e.target.closest("[data-q]");
      if (q && !q.disabled) {
        prefs.quality = q.getAttribute("data-q") === "auto" ? "auto" : Number(q.getAttribute("data-q"));
        persist();
        applyQuality();
        renderMoreMenu();
        return;
      }
      const ar = e.target.closest("[data-ar]");
      if (ar) {
        prefs.aspect = ar.getAttribute("data-ar");
        persist();
        applyAspect();
        renderMoreMenu();
        return;
      }
      const tog = e.target.closest("[data-toggle]");
      if (tog) {
        const kind = tog.getAttribute("data-toggle");
        if (kind === "mirror") {
          prefs.mirror = !prefs.mirror;
          persist();
          applyMirror();
        } else if (kind === "compressor") {
          if (!prefs.compressor && !ensureAudio()) {
            setErr("当前浏览器不支持音频压缩");
            return;
          }
          prefs.compressor = !prefs.compressor;
          persist();
          if (prefs.compressor) ensureAudio();
          applyCompressor();
        }
        renderMoreMenu();
      }
    });
    on(muteBtn, "click", function (e) {
      e.stopPropagation();
      closeMenus();
      toggleMute();
    });
    on(vol, "input", function () {
      const v = Number(vol.value) / 100;
      video.volume = v;
      video.muted = v === 0;
      setMuteUi();
    });
    on(fsBtn, "click", function (e) {
      e.stopPropagation();
      closeMenus();
      toggleFs();
    });
    on(seek, "input", function () {
      const d = mediaDuration();
      if (!(d > 0)) return;
      scrubTime = d * Number(seek.value) / 1000;
      paintPlayhead(scrubTime);
    });
    on(seek, "change", function () {
      const d = mediaDuration();
      if (d > 0) jumpTo(d * Number(seek.value) / 1000);
    });
    on(video, "play", setPlayingUi);
    on(video, "pause", setPlayingUi);
    on(video, "timeupdate", function () {
      updateProgress();
      snapSpeedIfLive();
    });
    on(video, "progress", updateProgress);
    on(video, "durationchange", updateProgress);
    on(video, "seeked", clearScrub);
    on(video, "playing", function () {
      if (!video.seeking) clearScrub();
    });
    on(video, "loadedmetadata", function () {
      applyAspect();
      applySpeed();
      applyStartAt();
      updateProgress();
      if (wantPlay) video.play().catch(function () {});
    });
    on(video, "volumechange", setMuteUi);
    on(root, "mousemove", function () {
      if (Date.now() - recentTouch < 800) return;
      pingIdle();
    });
    on(root.querySelector(".mt-controls"), "pointerdown", pingIdle);
    on(chrome, "click", function (e) { e.stopPropagation(); });
    on(document, "click", function (e) {
      if (!root.contains(e.target)) closeMenus();
    });
    on(document, "fullscreenchange", onFsChange);
    on(document, "webkitfullscreenchange", onFsChange);
    on(w, "resize", syncFsRotate);
    on(w, "orientationchange", syncFsRotate);
    on(document, "keydown", function (e) {
      if (typingTarget(e.target)) return;
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      if (menuOpen()) {
        if (e.key === "Escape") {
          e.preventDefault();
          closeMenus();
          pingIdle();
        }
        return;
      }
      switch (e.key) {
        case " ":
        case "Spacebar":
          e.preventDefault();
          togglePlay();
          break;
        case "ArrowLeft":
          e.preventDefault();
          seekBy(-10);
          break;
        case "ArrowRight":
          e.preventDefault();
          seekBy(10);
          break;
        case "ArrowUp":
          e.preventDefault();
          video.muted = false;
          video.volume = Math.min(1, video.volume + 0.1);
          vol.value = String(Math.round(video.volume * 100));
          break;
        case "ArrowDown":
          e.preventDefault();
          video.volume = Math.max(0, video.volume - 0.1);
          video.muted = video.volume === 0;
          vol.value = String(Math.round(video.volume * 100));
          break;
        case "m":
        case "M":
          e.preventDefault();
          toggleMute();
          break;
        case "f":
        case "F":
          e.preventDefault();
          toggleFs();
          break;
        default:
          if (e.key >= "0" && e.key <= "9") {
            e.preventDefault();
            seekPct(Number(e.key) * 0.1);
          }
      }
      pingIdle();
    });

    setPlayingUi();
    setMuteUi();
    setFsUi();
    pingIdle();

    return {
      video: video,
      load: function (opts) {
        opts = opts || {};
        setErr("");
        renditions = opts.renditions || [];
        metaW = opts.width || 0;
        metaH = opts.height || 0;
        liveMode = !!opts.live;
        startAt = opts.startAt || 0;
        wantPlay = !!opts.autoplay;
        onTimeupdateCb = typeof opts.onTimeupdate === "function" ? opts.onTimeupdate : null;
        onHlsError = typeof opts.onHlsError === "function" ? opts.onHlsError : null;
        if (opts.poster) video.poster = opts.poster;
        applyAspect();
        applyMirror();
        applySpeed();
        refreshMenus();
        if (liveMode) startLiveTick();
        else stopLiveTick();
        attach(opts.src);
        if (prefs.compressor) ensureAudio();
      },
      setLive: function (on) {
        liveMode = !!on;
        if (liveMode) startLiveTick();
        else stopLiveTick();
        updateProgress();
      },
      retryLoad: function () {
        if (hls) hls.startLoad();
      },
      destroy: function () {
        clearTimeout(idleTimer);
        clearTimeout(tapTimer);
        stopLiveTick();
        closeMenus();
        listeners.forEach(function (off) { off(); });
        if (hls) { hls.destroy(); hls = null; }
        if (audioCtx) audioCtx.close().catch(function () {});
        root.innerHTML = "";
      }
    };
  }

  w.mtPlayer = { mount: mount };
})(window);
