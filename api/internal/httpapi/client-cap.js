(function (g) {
  "use strict";

  const PROBE_MS = 3500;
  const HEIGHTS = [360, 720, 1080, 1440, 2160, 4320];
  const BITRATES = [2e6, 8e6, 20e6, 40e6, 80e6, 150e6];

  const FAMILIES = [
    {
      id: "h264",
      label: "H.264",
      mime: "video/mp4",
      profiles: [
        { profile: "Baseline", level: "3.0", codec: "avc1.42E01E" },
        { profile: "Main", level: "3.1", codec: "avc1.4D401F" },
        { profile: "High", level: "4.0", codec: "avc1.640028" },
        { profile: "High", level: "4.1", codec: "avc1.640029" },
        { profile: "High", level: "5.0", codec: "avc1.640032" },
        { profile: "High", level: "5.1", codec: "avc1.640033" },
        { profile: "High", level: "5.2", codec: "avc1.640034" }
      ]
    },
    {
      id: "hevc",
      label: "H.265",
      mime: "video/mp4",
      profiles: [
        { profile: "Main", level: "4.0", codec: "hvc1.1.6.L120.B0" },
        { profile: "Main", level: "5.0", codec: "hvc1.1.6.L150.B0" },
        { profile: "Main", level: "5.1", codec: "hvc1.1.6.L153.B0" },
        { profile: "Main", level: "5.2", codec: "hvc1.1.6.L156.B0" },
        { profile: "Main10", level: "5.1", codec: "hvc1.2.4.L153.B0" },
        { profile: "Main", level: "5.1", codec: "hev1.1.6.L153.B0" }
      ]
    },
    {
      id: "av1",
      label: "AV1",
      mime: "video/mp4",
      profiles: [
        { profile: "Main", level: "3.1", codec: "av01.0.05M.08" },
        { profile: "Main", level: "4.0", codec: "av01.0.08M.08" },
        { profile: "Main", level: "4.1", codec: "av01.0.09M.08" },
        { profile: "Main", level: "5.0", codec: "av01.0.12M.08" },
        { profile: "Main", level: "5.1", codec: "av01.0.13M.08" },
        { profile: "Main", level: "6.0", codec: "av01.0.16M.08" }
      ]
    },
    {
      id: "vp9",
      label: "VP9",
      mime: "video/webm",
      profiles: [
        { profile: "0", level: "3.1", codec: "vp09.00.31.08" },
        { profile: "0", level: "4.0", codec: "vp09.00.40.08" },
        { profile: "0", level: "5.0", codec: "vp09.00.50.08" },
        { profile: "0", level: "5.1", codec: "vp09.00.51.08" },
        { profile: "2", level: "5.1", codec: "vp09.02.51.10" }
      ]
    },
    {
      id: "vp8",
      label: "VP8",
      mime: "video/webm",
      profiles: [{ profile: "0", level: "", codec: "vp8" }]
    }
  ];

  function contentType(fam, codec) {
    return fam.mime + '; codecs="' + codec + '"';
  }

  function parseBrowser(ua) {
    ua = ua || "";
    const m = function (re) {
      const x = ua.match(re);
      return x ? x[1] : "";
    };
    if (/Edg(?:e|A|iOS)?\//.test(ua)) return { name: "edge", version: m(/Edg(?:e|A|iOS)?\/([\d.]+)/) };
    if (/OPR\/|Opera\//.test(ua)) return { name: "opera", version: m(/OPR\/([\d.]+)/) || m(/Opera\/([\d.]+)/) };
    if (/SamsungBrowser\//.test(ua)) return { name: "samsung", version: m(/SamsungBrowser\/([\d.]+)/) };
    if (/Firefox\/|FxiOS\//.test(ua)) return { name: "firefox", version: m(/FxiOS\/([\d.]+)/) || m(/Firefox\/([\d.]+)/) };
    if (/CriOS\//.test(ua)) return { name: "chrome", version: m(/CriOS\/([\d.]+)/) };
    if (/Chrome\//.test(ua) && !/ChromiumEdg/.test(ua)) return { name: "chrome", version: m(/Chrome\/([\d.]+)/) };
    if (/Safari\//.test(ua) && /Version\//.test(ua)) return { name: "safari", version: m(/Version\/([\d.]+)/) };
    if (/HuaweiBrowser\//.test(ua)) return { name: "huawei", version: m(/HuaweiBrowser\/([\d.]+)/) };
    if (/MiuiBrowser\//.test(ua)) return { name: "miui", version: m(/MiuiBrowser\/([\d.]+)/) };
    if (/UCBrowser\//.test(ua)) return { name: "uc", version: m(/UCBrowser\/([\d.]+)/) };
    return { name: "unknown", version: "" };
  }

  function parseOS(ua, platform, uadPlatform) {
    ua = ua || "";
    const plat = (uadPlatform || platform || "").toString();
    const p = plat.toLowerCase();
    if (/android/i.test(ua) || p === "android") {
      return { os: "android", osVersion: (ua.match(/Android ([\d.]+)/) || [])[1] || "" };
    }
    const iPadOS = /iPad/i.test(ua) || (p.indexOf("mac") >= 0 && (navigator.maxTouchPoints || 0) > 1 && !/Macintosh.*OS X 10/.test(ua) && /Mobile|iPad/.test(ua));
    if (/iPhone|iPod/i.test(ua) || p === "ios" || iPadOS || /iPad/i.test(ua)) {
      const ver = (ua.match(/OS ([\d_]+)/) || [])[1];
      return { os: /iPad/i.test(ua) || iPadOS ? "ipados" : "ios", osVersion: ver ? ver.replace(/_/g, ".") : "" };
    }
    if (/Windows/i.test(ua) || p.indexOf("win") === 0) {
      let ver = "";
      if (/Windows NT 10/.test(ua)) ver = "10+";
      else if (/Windows NT 6\.3/.test(ua)) ver = "8.1";
      else if (/Windows NT 6\.1/.test(ua)) ver = "7";
      return { os: "windows", osVersion: ver };
    }
    if (/CrOS/i.test(ua) || p.indexOf("chrome") >= 0) return { os: "chromeos", osVersion: "" };
    if (/Macintosh|Mac OS X/i.test(ua) || p.indexOf("mac") === 0) {
      const ver = (ua.match(/Mac OS X ([\d_]+)/) || [])[1];
      return { os: "macos", osVersion: ver ? ver.replace(/_/g, ".") : "" };
    }
    if (/Linux/i.test(ua) || p.indexOf("linux") === 0) return { os: "linux", osVersion: "" };
    return { os: plat || "unknown", osVersion: "" };
  }

  async function platformInfo() {
    const ua = navigator.userAgent || "";
    const uad = navigator.userAgentData;
    let high = null;
    if (uad && uad.getHighEntropyValues) {
      try {
        high = await uad.getHighEntropyValues([
          "architecture", "bitness", "model", "platformVersion", "fullVersionList", "uaFullVersion"
        ]);
      } catch (e) {}
    }
    const fromUA = parseBrowser(ua);
    let browser = fromUA.name;
    let browserVersion = fromUA.version;
    const brands = (high && high.fullVersionList) || (uad && uad.brands) || [];
    if (brands.length) {
      const skip = /not.?a.?brand/i;
      const pick = brands.find(function (b) { return /edg/i.test(b.brand); })
        || brands.find(function (b) { return /chrom/i.test(b.brand) && !skip.test(b.brand); })
        || brands.find(function (b) { return !skip.test(b.brand); });
      if (pick) {
        const b = String(pick.brand).toLowerCase();
        if (b.indexOf("edge") >= 0) browser = "edge";
        else if (b.indexOf("chrom") >= 0 && browser !== "edge") browser = "chrome";
        if (pick.version) browserVersion = pick.version;
      }
    }
    const osIn = parseOS(ua, navigator.platform || "", uad && uad.platform);
    if (high && high.platformVersion) osIn.osVersion = high.platformVersion;
    return {
      os: osIn.os,
      osVersion: osIn.osVersion,
      platform: (uad && uad.platform) || navigator.platform || "",
      architecture: (high && high.architecture) || "",
      bitness: (high && high.bitness) || "",
      model: (high && high.model) || "",
      mobile: !!(uad && uad.mobile) || /Mobile|Android|iPhone/i.test(ua),
      browser: browser,
      browserVersion: browserVersion,
      brands: brands,
      ua: ua
    };
  }

  function canPlay(type) {
    try {
      const el = document.createElement("video");
      return el.canPlayType(type) || "";
    } catch (e) {
      return "";
    }
  }

  function mseType(type) {
    try {
      return !!(window.MediaSource && MediaSource.isTypeSupported && MediaSource.isTypeSupported(type));
    } catch (e) {
      return false;
    }
  }

  function hevcUsableForHLS() {
    const hvc = 'video/mp4; codecs="hvc1.1.6.L120.B0"';
    const hev = 'video/mp4; codecs="hev1.1.6.L120.B0"';
    if (mseType(hvc) || mseType(hev)) return true;
    const ua = navigator.userAgent || "";
    const safari = /Safari/i.test(ua) && !/Chrome|CriOS|Chromium|Android/i.test(ua);
    if (!safari) return false;
    return !!(canPlay("application/vnd.apple.mpegurl") && (canPlay(hvc) || canPlay(hev)));
  }

  async function decoderSupport(codec, w, h, accel) {
    if (!g.VideoDecoder || !VideoDecoder.isConfigSupported) return null;
    try {
      const cfg = { codec: codec, codedWidth: w, codedHeight: h };
      if (accel) cfg.hardwareAcceleration = accel;
      const r = await VideoDecoder.isConfigSupported(cfg);
      return !!(r && r.supported);
    } catch (e) {
      return null;
    }
  }

  async function decodingInfo(type, w, h, bitrate, fps) {
    if (!navigator.mediaCapabilities || !navigator.mediaCapabilities.decodingInfo) return null;
    try {
      const r = await navigator.mediaCapabilities.decodingInfo({
        type: "media-source",
        video: {
          contentType: type,
          width: w,
          height: h,
          bitrate: bitrate,
          framerate: fps
        }
      });
      return {
        supported: !!r.supported,
        smooth: !!r.smooth,
        powerEfficient: !!r.powerEfficient
      };
    } catch (e) {
      return null;
    }
  }

  function sizeForHeight(h) {
    return { width: Math.round(h * 16 / 9), height: h };
  }

  async function probeFamily(fam) {
    const out = {
      id: fam.id,
      label: fam.label,
      software: false,
      hardware: false,
      canPlay: "",
      mediaSource: false,
      profiles: [],
      max: null
    };
    const first = fam.profiles[0];
    const t0 = contentType(fam, first.codec);
    out.canPlay = canPlay(t0);
    out.mediaSource = mseType(t0);

    for (let i = 0; i < fam.profiles.length; i++) {
      const p = fam.profiles[i];
      const type = contentType(fam, p.codec);
      const sz = sizeForHeight(p.level && Number(p.level) >= 5 ? 2160 : 1080);
      const hw = await decoderSupport(p.codec, sz.width, sz.height, "prefer-hardware");
      const sw = await decoderSupport(p.codec, sz.width, sz.height, "prefer-software");
      const any = await decoderSupport(p.codec, sz.width, sz.height, "");
      const mc = await decodingInfo(type, sz.width, sz.height, 8e6, 30);
      const row = {
        profile: p.profile,
        level: p.level,
        codec: p.codec,
        canPlay: canPlay(type),
        mediaSource: mseType(type),
        videoDecoder: { hardware: hw, software: sw, any: any },
        mediaCapabilities: mc
      };
      out.profiles.push(row);
      if (hw === true || (mc && mc.powerEfficient && mc.supported)) out.hardware = true;
      if (sw === true || any === true || (mc && mc.supported) || row.canPlay || row.mediaSource) out.software = true;
    }

    if (out.hardware || out.software) {
      let best = null;
      const codec = (out.profiles.find(function (p) {
        return p.videoDecoder.hardware === true || (p.mediaCapabilities && p.mediaCapabilities.powerEfficient);
      }) || out.profiles[0]).codec;
      const type = contentType(fam, codec);
      for (let i = 0; i < HEIGHTS.length; i++) {
        const sz = sizeForHeight(HEIGHTS[i]);
        const hw = await decoderSupport(codec, sz.width, sz.height, "prefer-hardware");
        const mc = await decodingInfo(type, sz.width, sz.height, 8e6, 30);
        const okHW = hw === true || (mc && mc.supported && mc.powerEfficient);
        const okAny = hw === true || (mc && mc.supported) || (await decoderSupport(codec, sz.width, sz.height, "")) === true;
        if (!okAny && !okHW) break;
        if (out.hardware && !okHW && HEIGHTS[i] > 1080) break;
        best = {
          width: sz.width,
          height: sz.height,
          bitrate: 8e6,
          framerate: 30,
          codec: codec,
          hardware: !!okHW,
          mediaCapabilities: mc
        };
      }
      if (best) {
        let maxBr = 8e6;
        for (let i = 0; i < BITRATES.length; i++) {
          const mc = await decodingInfo(type, best.width, best.height, BITRATES[i], 30);
          if (mc && mc.supported) maxBr = BITRATES[i];
          else if (mc && !mc.supported) break;
        }
        best.bitrate = maxBr;
        const prof = out.profiles.find(function (p) { return p.codec === codec; });
        if (prof) {
          best.profile = prof.profile;
          best.level = prof.level;
        }
        out.max = best;
      }
    }
    return out;
  }

  async function probeCodecs() {
    const list = [];
    for (let i = 0; i < FAMILIES.length; i++) list.push(await probeFamily(FAMILIES[i]));
    return list;
  }

  async function probe() {
    const t0 = performance.now();
    const plat = await platformInfo();
    const report = {
      os: plat.os,
      osVersion: plat.osVersion,
      platform: plat.platform,
      architecture: plat.architecture,
      bitness: plat.bitness,
      model: plat.model,
      mobile: plat.mobile,
      browser: plat.browser,
      browserVersion: plat.browserVersion,
      brands: plat.brands,
      ua: plat.ua,
      screen: {
        width: screen.width || 0,
        height: screen.height || 0,
        availWidth: screen.availWidth || 0,
        availHeight: screen.availHeight || 0,
        devicePixelRatio: g.devicePixelRatio || 1
      },
      apis: {
        videoDecoder: !!(g.VideoDecoder && VideoDecoder.isConfigSupported),
        mediaCapabilities: !!(navigator.mediaCapabilities && navigator.mediaCapabilities.decodingInfo),
        mediaSource: !!g.MediaSource,
        userAgentData: !!navigator.userAgentData
      },
      hwCodecs: [],
      codecs: [],
      probeMs: 0
    };
    try {
      const codecs = await Promise.race([
        probeCodecs(),
        new Promise(function (resolve) { setTimeout(function () { resolve(null); }, PROBE_MS); })
      ]);
      if (codecs) {
        report.codecs = codecs;
        report.hwCodecs = codecs.filter(function (c) {
          if (!c.hardware) return false;
          if (c.id === "hevc" && !hevcUsableForHLS()) return false;
          return true;
        }).map(function (c) { return c.id; });
      } else {
        report.probeTimedOut = true;
      }
    } catch (e) {
      report.probeError = String(e && e.message ? e.message : e);
    }
    report.probeMs = Math.round(performance.now() - t0);
    return report;
  }

  let memo = null;
  function get() {
    if (!memo) memo = probe();
    return memo;
  }

  async function fetchPlayback(api, videoId, headers, extra) {
    const client = Object.assign({}, await get(), extra || {});
    const h = Object.assign({ "Content-Type": "application/json" }, headers || {});
    const r = await fetch((api || "") + "/v1/videos/" + videoId + "/playback", {
      method: "POST",
      headers: h,
      cache: "no-store",
      body: JSON.stringify({ client: client })
    });
    const data = await r.json().catch(function () { return {}; });
    if (!r.ok) throw new Error(data.message || "视频不可用");
    return data;
  }

  g.mtClientCap = { get: get, fetchPlayback: fetchPlayback };
})(typeof window !== "undefined" ? window : self);
