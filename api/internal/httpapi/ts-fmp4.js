(function (g) {
  "use strict";

  const TS = 188;
  const TIMESCALE = 90000;
  const ADTS_FREQ = [96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350];

  function slog() {
    try { console.log.apply(console, ["[mt-ts]"].concat([].slice.call(arguments))); } catch (e) {}
  }

  function concat(chunks) {
    let n = 0;
    for (let i = 0; i < chunks.length; i++) n += chunks[i].length;
    const o = new Uint8Array(n);
    let p = 0;
    for (let i = 0; i < chunks.length; i++) { o.set(chunks[i], p); p += chunks[i].length; }
    return o;
  }

  function u32(n) {
    return [(n >>> 24) & 255, (n >>> 16) & 255, (n >>> 8) & 255, n & 255];
  }

  function box(type, payload) {
    const n = 8 + payload.length;
    const o = new Uint8Array(n);
    o.set(u32(n), 0);
    o[4] = type.charCodeAt(0); o[5] = type.charCodeAt(1);
    o[6] = type.charCodeAt(2); o[7] = type.charCodeAt(3);
    o.set(payload, 8);
    return o;
  }

  function splitAnnexB(es) {
    const nals = [];
    const starts = [];
    for (let i = 0; i + 3 < es.length; i++) {
      if (es[i] === 0 && es[i + 1] === 0) {
        if (es[i + 2] === 1) { starts.push({ at: i, skip: 3 }); i += 2; }
        else if (i + 3 < es.length && es[i + 2] === 0 && es[i + 3] === 1) { starts.push({ at: i, skip: 4 }); i += 3; }
      }
    }
    for (let i = 0; i < starts.length; i++) {
      const a = starts[i].at + starts[i].skip;
      const b = i + 1 < starts.length ? starts[i + 1].at : es.length;
      if (b > a) nals.push({ nal: es.subarray(a, b), start: starts[i].at });
    }
    return nals;
  }

  function hevcType(nal) { return nal.length ? (nal[0] >> 1) & 0x3f : 0; }
  function avcType(nal) { return nal.length ? nal[0] & 0x1f : 0; }
  function hevcVCL(t) { return t < 32; }
  function hevcFirstSlice(nal) {
    const t = hevcType(nal);
    if (!hevcVCL(t) || nal.length < 3) return false;
    return (nal[2] & 0x80) !== 0;
  }

  function parseTS(buf) {
    const u8 = buf instanceof Uint8Array ? buf : new Uint8Array(buf);
    const pmtPid = Object.create(null);
    const types = Object.create(null);
    let pmtKnown = false;
    const pes = Object.create(null);
    const videoChunks = [];
    const audioChunks = [];
    const videoPtsAt = [];
    const audioPtsAt = [];
    let videoPid = -1, audioPid = -1;

    function flushPes(pid) {
      const st = pes[pid];
      if (!st || !st.parts.length) return;
      const raw = concat(st.parts);
      st.parts = [];
      if (raw.length < 9 || raw[0] !== 0 || raw[1] !== 0 || raw[2] !== 1) return;
      const sid = raw[3];
      const hdr = 9 + raw[8];
      if (raw.length < hdr) return;
      function pesTs(o) {
        return ((raw[o] & 0x0e) << 29) | (raw[o + 1] << 22) | ((raw[o + 2] & 0xfe) << 14) | (raw[o + 3] << 7) | ((raw[o + 4] & 0xfe) >> 1);
      }
      let pts = null, dts = null;
      const flags = raw[7];
      if ((flags & 0x80) && hdr >= 14) pts = pesTs(9);
      if ((flags & 0x40) && hdr >= 19) dts = pesTs(14);
      if (dts == null) dts = pts;
      const payload = raw.subarray(hdr);
      const typ = types[pid];
      if (typ === 0x24 || typ === 0x27 || typ === 0x1b || (sid >= 0xe0 && sid <= 0xef)) {
        if (videoPid < 0) videoPid = pid;
        if (pts != null || dts != null) {
          videoPtsAt.push({
            off: videoChunks.reduce(function (n, c) { return n + c.length; }, 0),
            pts: pts != null ? pts : dts,
            dts: dts != null ? dts : pts
          });
        }
        videoChunks.push(payload);
      } else if (typ === 0x0f || typ === 0x11 || (sid >= 0xc0 && sid <= 0xdf)) {
        if (audioPid < 0) audioPid = pid;
        if (pts != null || dts != null) {
          audioPtsAt.push({
            off: audioChunks.reduce(function (n, c) { return n + c.length; }, 0),
            pts: pts != null ? pts : dts,
            dts: dts != null ? dts : pts
          });
        }
        audioChunks.push(payload);
      }
    }

    for (let off = 0; off + TS <= u8.length; off += TS) {
      if (u8[off] !== 0x47) continue;
      const tei = u8[off + 1];
      const pid = ((tei & 0x1f) << 8) | u8[off + 2];
      const afc = (u8[off + 3] >> 4) & 3;
      const pusi = (tei & 0x40) !== 0;
      let i = off + 4;
      if (afc & 2) i += 1 + u8[i];
      if (i >= off + TS) continue;
      const payload = u8.subarray(i, off + TS);
      if (pid === 0 && pusi && payload.length > 8) {
        const p = 1 + payload[0];
        if (p + 8 < payload.length && payload[p] === 0x00) {
          const sectionLen = ((payload[p + 1] & 0x0f) << 8) | payload[p + 2];
          let q = p + 8;
          const end = Math.min(payload.length, p + 3 + sectionLen - 4);
          while (q + 4 <= end) {
            const prog = (payload[q] << 8) | payload[q + 1];
            const mpid = ((payload[q + 2] & 0x1f) << 8) | payload[q + 3];
            if (prog !== 0) pmtPid[mpid] = true;
            q += 4;
          }
        }
        continue;
      }
      if (pmtPid[pid] && pusi && payload.length > 1) {
        let p = 1 + payload[0];
        if (p + 12 < payload.length && payload[p] === 0x02) {
          const pil = ((payload[p + 10] & 0x0f) << 8) | payload[p + 11];
          let q = p + 12 + pil;
          while (q + 5 <= payload.length) {
            const st = payload[q];
            const epid = ((payload[q + 1] & 0x1f) << 8) | payload[q + 2];
            const eil = ((payload[q + 3] & 0x0f) << 8) | payload[q + 4];
            types[epid] = st;
            if (st === 0x24 || st === 0x27 || st === 0x1b) videoPid = epid;
            if (st === 0x0f || st === 0x11) audioPid = epid;
            q += 5 + eil;
            pmtKnown = true;
          }
        }
      }
      if (!pes[pid]) pes[pid] = { parts: [] };
      if (pusi) flushPes(pid);
      pes[pid].parts.push(payload);
    }
    Object.keys(pes).forEach(function (k) { flushPes(Number(k)); });
    const hevc = types[videoPid] === 0x24 || types[videoPid] === 0x27;
    const avc = types[videoPid] === 0x1b;
    slog("ts", "bytes=" + u8.length, "video=" + videoChunks.reduce(function (n, c) { return n + c.length; }, 0), "hevc=" + hevc, "avc=" + avc, "vPid=" + videoPid, "aPid=" + audioPid, "pmt=" + pmtKnown);
    return {
      hevc: hevc,
      avc: avc,
      video: concat(videoChunks),
      audio: concat(audioChunks),
      videoPtsAt: videoPtsAt,
      audioPtsAt: audioPtsAt
    };
  }

  function clockAt(map, off) {
    let pts = 0, dts = 0;
    if (map.length) { pts = map[0].pts; dts = map[0].dts != null ? map[0].dts : map[0].pts; }
    for (let i = 0; i < map.length; i++) {
      if (map[i].off <= off) {
        pts = map[i].pts;
        dts = map[i].dts != null ? map[i].dts : map[i].pts;
      } else break;
    }
    return { pts: pts, dts: dts };
  }

  function hevcIrap(t) { return t >= 16 && t <= 21; }

  function groupAUs(hevc, nals, ptsMap) {
    const samples = [];
    let cur = [], curPts = 0, curDts = 0, sawVCL = false;
    function flush() {
      if (!cur.length) return;
      samples.push({ nals: cur, pts: curPts, dts: curDts });
      cur = []; sawVCL = false;
    }
    for (let i = 0; i < nals.length; i++) {
      const nal = nals[i].nal;
      const t = hevc ? hevcType(nal) : avcType(nal);
      const vcl = hevc ? hevcVCL(t) : (t === 1 || t === 5);
      const boundary = hevc
        ? (t === 35 || (vcl && hevcFirstSlice(nal) && sawVCL))
        : ((t === 9) || (vcl && sawVCL));
      if (boundary) flush();
      if (!cur.length) {
        const ck = clockAt(ptsMap, nals[i].start);
        curPts = ck.pts;
        curDts = ck.dts;
      }
      cur.push(nal);
      if (vcl) sawVCL = true;
    }
    flush();
    return samples;
  }

  function lengthPrefixed(nals, skip) {
    const parts = [];
    for (let i = 0; i < nals.length; i++) {
      if (skip && skip(nals[i])) continue;
      const n = nals[i];
      const h = new Uint8Array(4 + n.length);
      h[0] = (n.length >>> 24) & 255; h[1] = (n.length >>> 16) & 255;
      h[2] = (n.length >>> 8) & 255; h[3] = n.length & 255;
      h.set(n, 4);
      parts.push(h);
    }
    return concat(parts);
  }

  function avcC(sps, pps) {
    const s = sps[0], p = pps[0];
    const body = [1, s[1], s[2], s[3], 0xff, 0xe1, (s.length >> 8) & 255, s.length & 255];
    const o = new Uint8Array(body.length + s.length + 3 + p.length);
    o.set(body, 0);
    o.set(s, body.length);
    let i = body.length + s.length;
    o[i++] = 1; o[i++] = (p.length >> 8) & 255; o[i++] = p.length & 255;
    o.set(p, i);
    return box("avcC", o);
  }

  function nalRbsp(nal) {
    const src = nal.subarray(2);
    const out = [];
    for (let i = 0; i < src.length; i++) {
      if (i + 2 < src.length && src[i] === 0 && src[i + 1] === 0 && src[i + 2] === 3) {
        out.push(0, 0);
        i += 2;
      } else out.push(src[i]);
    }
    return new Uint8Array(out);
  }

  function BitReader(u8) {
    this.d = u8;
    this.p = 0;
  }
  BitReader.prototype.u = function (n) {
    let v = 0;
    while (n--) {
      const b = this.d[this.p >> 3] || 0;
      v = (v << 1) | ((b >> (7 - (this.p & 7))) & 1);
      this.p++;
    }
    return v >>> 0;
  };
  BitReader.prototype.ue = function () {
    let z = 0;
    while (this.p >> 3 < this.d.length && this.u(1) === 0) z++;
    if (z === 0) return 0;
    return ((1 << z) | this.u(z)) - 1;
  };

  function parseHevcSps(nal) {
    try {
      const b = new BitReader(nalRbsp(nal));
      b.u(4);
      const maxSub = b.u(3);
      b.u(1);
      const ptl = {
        space: b.u(2),
        tier: b.u(1),
        idc: b.u(5),
        compat: b.u(32),
        constraint: [b.u(8), b.u(8), b.u(8), b.u(8), b.u(8), b.u(8)],
        level: b.u(8)
      };
      const subP = [], subL = [];
      for (let i = 0; i < maxSub; i++) {
        subP[i] = b.u(1);
        subL[i] = b.u(1);
      }
      if (maxSub > 0) {
        for (let i = maxSub; i < 8; i++) b.u(2);
      }
      for (let i = 0; i < maxSub; i++) {
        if (subP[i]) {
          b.u(32); b.u(32); b.u(24);
        }
        if (subL[i]) b.u(8);
      }
      b.ue();
      const chroma = b.ue();
      if (chroma === 3) b.u(1);
      const w = b.ue();
      const h = b.ue();
      let dw = w, dh = h;
      if (b.u(1)) {
        const left = b.ue(), right = b.ue(), top = b.ue(), bottom = b.ue();
        const sw = chroma === 1 || chroma === 2 ? 2 : 1;
        const sh = chroma === 1 ? 2 : 1;
        dw = w - (left + right) * sw;
        dh = h - (top + bottom) * sh;
      }
      const lumaM8 = b.ue();
      const chromaM8 = b.ue();
      return {
        w: dw, h: dh, chroma: chroma, ptl: ptl,
        bitDepthLumaMinus8: lumaM8, bitDepthChromaMinus8: chromaM8
      };
    } catch (e) {
      return null;
    }
  }

  function hvcC(vps, sps, pps) {
    const arrays = [];
    function add(t, list) {
      if (!list.length) return;
      const parts = [new Uint8Array([t | 0x80, 0, list.length])];
      for (let i = 0; i < list.length; i++) {
        const n = list[i];
        const h = new Uint8Array(2 + n.length);
        h[0] = (n.length >> 8) & 255;
        h[1] = n.length & 255;
        h.set(n, 2);
        parts.push(h);
      }
      arrays.push(concat(parts));
    }
    add(32, vps);
    add(33, sps);
    add(34, pps);
    const info = sps[0] ? parseHevcSps(sps[0]) : null;
    const ptl = (info && info.ptl) || {
      space: 0, tier: 0, idc: 1, compat: 0x60000000,
      constraint: [0x90, 0, 0, 0, 0, 0], level: 93
    };
    const chroma = (info && info.chroma) || 1;
    let lumaM8 = info && info.bitDepthLumaMinus8 != null ? info.bitDepthLumaMinus8 : (ptl.idc >= 2 ? 2 : 0);
    let chromaM8 = info && info.bitDepthChromaMinus8 != null ? info.bitDepthChromaMinus8 : lumaM8;
    if (lumaM8 > 6) lumaM8 = ptl.idc >= 2 ? 2 : 0;
    if (chromaM8 > 6) chromaM8 = lumaM8;
    const cfg = new Uint8Array(23);
    cfg[0] = 1;
    cfg[1] = ((ptl.space & 3) << 6) | ((ptl.tier & 1) << 5) | (ptl.idc & 31);
    cfg[2] = (ptl.compat >>> 24) & 255;
    cfg[3] = (ptl.compat >>> 16) & 255;
    cfg[4] = (ptl.compat >>> 8) & 255;
    cfg[5] = ptl.compat & 255;
    for (let i = 0; i < 6; i++) cfg[6 + i] = ptl.constraint[i] || 0;
    cfg[12] = ptl.level;
    cfg[13] = 0xf0;
    cfg[14] = 0x00;
    cfg[15] = 0xfc;
    cfg[16] = 0xfc | (chroma & 3);
    cfg[17] = 0xf8 | (lumaM8 & 7);
    cfg[18] = 0xf8 | (chromaM8 & 7);
    cfg[21] = 0x03;
    cfg[22] = arrays.length;
    slog("hvcC", "w=" + (info && info.w), "h=" + (info && info.h), "idc=" + ptl.idc, "level=" + ptl.level, "bit=" + (8 + lumaM8), "arrays=" + arrays.length);
    return { box: box("hvcC", concat([cfg].concat(arrays))), info: info, ptl: ptl };
  }

  function rfc6381Hevc(ptl) {
    ptl = ptl || {};
    const idc = ptl.idc || 1;
    let flags = (ptl.compat >>> 0) || 0x60000000;
    let hex = flags.toString(16).replace(/0+$/, "") || "0";
    const tier = ptl.tier ? "H" : "L";
    const level = ptl.level || 93;
    return "hvc1." + idc + "." + hex + "." + tier + level + ".B0";
  }

  function parseADTS(buf) {
    const frames = [];
    let i = 0;
    while (i + 7 < buf.length) {
      if (buf[i] !== 0xff || (buf[i + 1] & 0xf0) !== 0xf0) { i++; continue; }
      const len = ((buf[i + 3] & 3) << 11) | (buf[i + 4] << 3) | ((buf[i + 5] >> 5) & 7);
      if (len < 7 || i + len > buf.length) break;
      const freqI = (buf[i + 2] >> 2) & 0xf;
      const ch = ((buf[i + 2] & 1) << 2) | ((buf[i + 3] >> 6) & 3);
      const hdr = (buf[i + 1] & 1) ? 7 : 9;
      frames.push({
        data: buf.subarray(i + hdr, i + len),
        off: i,
        freq: ADTS_FREQ[freqI] || 44100,
        ch: ch || 2,
        obj: (buf[i + 2] >> 6) & 3,
        freqI: freqI
      });
      i += len;
    }
    return frames;
  }

  function ftyp(kind) {
    const iso5 = [105, 115, 111, 53];
    const iso6 = [105, 115, 111, 54];
    const mp41 = [109, 112, 52, 49];
    let extra;
    if (kind === "hevc") extra = [104, 118, 99, 49];
    else if (kind === "audio") extra = [77, 52, 65, 32];
    else extra = [97, 118, 99, 49];
    return box("ftyp", concat([new Uint8Array(iso5), new Uint8Array([0, 0, 0, 0]), new Uint8Array(iso5.concat(iso6).concat(mp41).concat(extra))]));
  }

  function writeU32(b, off, n) {
    b[off] = (n >>> 24) & 255;
    b[off + 1] = (n >>> 16) & 255;
    b[off + 2] = (n >>> 8) & 255;
    b[off + 3] = n & 255;
  }

  function unityMatrix(b, off) {
    writeU32(b, off, 0x00010000);
    writeU32(b, off + 16, 0x00010000);
    writeU32(b, off + 32, 0x40000000);
  }

  function mvhd(nextId) {
    const b = new Uint8Array(100);
    writeU32(b, 12, TIMESCALE);
    b[20] = 0; b[21] = 1; b[22] = 0; b[23] = 0;
    b[24] = 1; b[25] = 0;
    unityMatrix(b, 36);
    writeU32(b, 96, nextId || 2);
    return box("mvhd", b);
  }

  function tkhd(id, w, h, isVideo) {
    const b = new Uint8Array(84);
    b[3] = 7;
    writeU32(b, 12, id);
    if (!isVideo) b[36] = 1;
    unityMatrix(b, 40);
    if (isVideo) {
      writeU32(b, 76, (w << 16) >>> 0);
      writeU32(b, 80, (h << 16) >>> 0);
    }
    return box("tkhd", b);
  }

  function mdhd(timescale) {
    const b = new Uint8Array(24);
    writeU32(b, 12, timescale);
    b[20] = 0x55; b[21] = 0xc4;
    return box("mdhd", b);
  }

  function hdlr(name) {
    const type = name === "vide" ? "vide" : "soun";
    const label = name === "vide" ? "VideoHandler" : "SoundHandler";
    const b = new Uint8Array(24 + label.length + 1);
    b[8] = type.charCodeAt(0); b[9] = type.charCodeAt(1);
    b[10] = type.charCodeAt(2); b[11] = type.charCodeAt(3);
    for (let i = 0; i < label.length; i++) b[24 + i] = label.charCodeAt(i);
    return box("hdlr", b);
  }

  function vmhd() { return box("vmhd", new Uint8Array([0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0])); }
  function smhd() { return box("smhd", new Uint8Array(8)); }
  function dinf() {
    return box("dinf", box("dref", concat([new Uint8Array([0, 0, 0, 0, 0, 0, 0, 1]), box("url ", new Uint8Array([0, 0, 0, 1]))])));
  }

  function stsdVideo(hevc, w, h, dec) {
    const inner = concat([
      new Uint8Array(6), new Uint8Array([0, 1]),
      new Uint8Array(16),
      new Uint8Array([(w >> 8) & 255, w & 255, (h >> 8) & 255, h & 255]),
      new Uint8Array([0, 0x48, 0, 0, 0, 0x48, 0, 0, 0, 0, 0, 0, 0, 1]),
      new Uint8Array(32),
      new Uint8Array([0, 0x18, 0xff, 0xff]),
      dec
    ]);
    const entry = box(hevc ? "hvc1" : "avc1", inner);
    return box("stsd", concat([new Uint8Array([0, 0, 0, 0, 0, 0, 0, 1]), entry]));
  }

  function stsdAudio(freq, ch, asc) {
    const n = asc.length;
    const esds = box("esds", concat([
      new Uint8Array([
        0, 0, 0, 0,
        0x03, 0x17 + n, 0, 0, 0,
        0x04, 0x0f + n, 0x40, 0x15, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
        0x05, n
      ]),
      asc,
      new Uint8Array([0x06, 0x01, 0x02])
    ]));
    const rate = (freq << 16) >>> 0;
    const mp4a = box("mp4a", concat([
      new Uint8Array(6), new Uint8Array([0, 1]),
      new Uint8Array(8),
      new Uint8Array([0, ch, 0, 0x10, 0, 0, 0, 0]),
      new Uint8Array([(rate >>> 24) & 255, (rate >>> 16) & 255, (rate >>> 8) & 255, rate & 255]),
      esds
    ]));
    return box("stsd", concat([new Uint8Array([0, 0, 0, 0, 0, 0, 0, 1]), mp4a]));
  }

  function stbl(stsd) {
    const empty = box("stts", new Uint8Array(8));
    const stsc = box("stsc", new Uint8Array(8));
    const stsz = box("stsz", new Uint8Array(12));
    const stco = box("stco", new Uint8Array(8));
    return box("stbl", concat([stsd, empty, stsc, stsz, stco]));
  }

  function minf(isVideo, stsd) {
    return box("minf", concat([isVideo ? vmhd() : smhd(), dinf(), stbl(stsd)]));
  }

  function mdia(isVideo, timescale, stsd) {
    return box("mdia", concat([mdhd(timescale), hdlr(isVideo ? "vide" : "soun"), minf(isVideo, stsd)]));
  }

  function trak(id, isVideo, w, h, timescale, stsd) {
    return box("trak", concat([tkhd(id, w, h, isVideo), mdia(isVideo, timescale, stsd)]));
  }

  function trex(id) {
    const b = new Uint8Array(24);
    b[7] = id; b[11] = 1;
    return box("trex", b);
  }

  function aacASC(audio) {
    return new Uint8Array([
      ((audio.obj + 1) << 3) | ((audio.freqI >> 1) & 7),
      ((audio.freqI & 1) << 7) | ((audio.ch & 15) << 3)
    ]);
  }

  function moovVideo(hevc, w, h, vdec) {
    return box("moov", concat([
      mvhd(2),
      trak(1, true, w, h, TIMESCALE, stsdVideo(hevc, w, h, vdec)),
      box("mvex", trex(1))
    ]));
  }

  function moovAV(hevc, w, h, vdec, audio) {
    const asc = aacASC(audio);
    return box("moov", concat([
      mvhd(3),
      trak(1, true, w, h, TIMESCALE, stsdVideo(hevc, w, h, vdec)),
      trak(2, false, 0, 0, audio.freq || 44100, stsdAudio(audio.freq, audio.ch, asc)),
      box("mvex", concat([trex(1), trex(2)]))
    ]));
  }

  function moovAudio(audio) {
    const asc = aacASC(audio);
    return box("moov", concat([
      mvhd(2),
      trak(1, false, 0, 0, audio.freq || 44100, stsdAudio(audio.freq, audio.ch, asc)),
      box("mvex", trex(1))
    ]));
  }

  function moof(seq, trackId, base, samples, dataOffset) {
    const tfhd = box("tfhd", new Uint8Array([0, 2, 0, 0, 0, 0, 0, trackId]));
    const tfdt = box("tfdt", concat([new Uint8Array([1, 0, 0, 0]), u32(Math.floor(base / 0x100000000)), u32(base >>> 0)]));
    let ver = 0;
    for (let i = 0; i < samples.length; i++) if (samples[i].cts < 0) ver = 1;
    const trunBody = [ver, 0, 0x0f, 1, ...u32(samples.length), ...u32(dataOffset)];
    const tb = [];
    samples.forEach(function (s) {
      tb.push(u32(s.dur), u32(s.size), u32(s.flags), u32(s.cts >>> 0));
    });
    const trun = box("trun", concat([new Uint8Array(trunBody)].concat(tb.map(function (x) { return new Uint8Array(x); }))));
    const traf = box("traf", concat([tfhd, tfdt, trun]));
    const mfhd = box("mfhd", concat([new Uint8Array(4), u32(seq)]));
    return box("moof", concat([mfhd, traf]));
  }

  function remuxSegment(buf, st) {
    st = st || {};
    const ts = parseTS(buf);
    const nals = splitAnnexB(ts.video);
    let hevc = st.hevc != null ? st.hevc : ts.hevc;
    if (!hevc && !ts.avc && nals.length) {
      const t0 = hevcType(nals[0].nal);
      if (t0 >= 32 && t0 <= 40) hevc = true;
    }
    const samplesIn = groupAUs(hevc, nals, ts.videoPtsAt);
    slog("remux", "nals=" + nals.length, "au=" + samplesIn.length, "hevc=" + hevc, "es=" + ts.video.length,
      "t0=" + (samplesIn[0] && samplesIn[0].nals.map(hevc ? hevcType : avcType).join(",")));
    if (!samplesIn.length) return { init: null, frag: null, duration: 0, state: st };

    const vps = [], sps = [], pps = [];
    samplesIn.forEach(function (s) {
      s.nals.forEach(function (n) {
        if (hevc) {
          const t = hevcType(n);
          if (t === 32 && !vps.length) vps.push(n);
          if (t === 33 && !sps.length) sps.push(n);
          if (t === 34 && !pps.length) pps.push(n);
        } else {
          const t = avcType(n);
          if (t === 7 && !sps.length) sps.push(n);
          if (t === 8 && !pps.length) pps.push(n);
        }
      });
    });
    if (sps.length) st.sps = sps;
    if (pps.length) st.pps = pps;
    if (vps.length) st.vps = vps;
    st.hevc = hevc;
    const useSps = st.sps || sps, usePps = st.pps || pps, useVps = st.vps || vps;
    if (!useSps || !useSps.length) return { init: null, frag: null, duration: 0, state: st };

    let w = st.w || 1280, h = st.h || 720;
    let dec = avcC(useSps, usePps);
    let codecs = "avc1.4d401f";
    if (hevc) {
      const hv = hvcC(useVps, useSps, usePps);
      dec = hv.box;
      codecs = rfc6381Hevc(hv.ptl);
      if (hv.info && hv.info.w >= 16 && hv.info.w <= 8192 && hv.info.h >= 16 && hv.info.h <= 8192) {
        w = hv.info.w;
        h = hv.info.h;
      }
      const lumaM8 = hv.info && hv.info.bitDepthLumaMinus8 != null ? hv.info.bitDepthLumaMinus8 : 0;
      if (lumaM8 > 0) {
        slog("hevc 10bit", "bit=" + (8 + lumaM8), w + "x" + h);
        throw new Error("hevc 10bit");
      }
    }
    st.w = w;
    st.h = h;

    const skip = function (n) {
      if (hevc) { const t = hevcType(n); return t === 32 || t === 33 || t === 34 || t === 35; }
      const t = avcType(n); return t === 7 || t === 8 || t === 9;
    };
    const rawA = ts.audio || new Uint8Array(0);
    const aFrames = parseADTS(rawA);
    if (aFrames.length) {
      const freq = aFrames[0].freq || 44100;
      const frameDur = Math.max(1, Math.round(1024 * TIMESCALE / freq));
      let lastPes = null, acc = 0;
      aFrames.forEach(function (f) {
        const ck = clockAt(ts.audioPtsAt || [], f.off);
        if (ck.pts !== lastPes) { lastPes = ck.pts; acc = 0; }
        f.pts = (ck.pts || (samplesIn[0] && samplesIn[0].pts) || 0) + acc;
        f.dur = frameDur;
        acc += frameDur;
      });
      if (!st.audio) {
        st.audio = { freq: freq, ch: aFrames[0].ch, obj: aFrames[0].obj, freqI: aFrames[0].freqI };
      }
    }
    const v0 = samplesIn[0].dts != null ? samplesIn[0].dts : samplesIn[0].pts;
    if (st.base == null) {
      st.base = (aFrames.length && aFrames[0].pts < v0) ? aFrames[0].pts : v0;
    }
    const vSamples = [];
    const mdatParts = [];
    let keys = 0;
    let lastDts = samplesIn[0].dts != null ? samplesIn[0].dts : samplesIn[0].pts;
    samplesIn.forEach(function (s, i) {
      const dts = s.dts != null ? s.dts : s.pts;
      const next = i + 1 < samplesIn.length
        ? (samplesIn[i + 1].dts != null ? samplesIn[i + 1].dts : samplesIn[i + 1].pts)
        : dts + Math.round(TIMESCALE / 30);
      let dur = next - dts;
      if (dur <= 0) dur = Math.round(TIMESCALE / 30);
      const payload = lengthPrefixed(s.nals, skip);
      if (!payload.length) return;
      const key = s.nals.some(function (n) {
        const t = hevc ? hevcType(n) : avcType(n);
        return hevc ? hevcIrap(t) : t === 5;
      });
      if (key) keys++;
      vSamples.push({
        dur: dur, size: payload.length, cts: (s.pts - dts) | 0,
        flags: key ? 0x02000000 : 0x01010000
      });
      mdatParts.push(payload);
      lastDts = next;
    });
    if (!st.inited) slog("samples", "n=" + vSamples.length, "keys=" + keys, "cts0=" + (vSamples[0] && vSamples[0].cts));
    if (!vSamples.length) return { init: null, frag: null, duration: 0, state: st, codecs: codecs };
    const vData = concat(mdatParts);

    const useAudio = !!(aFrames.length && st.audio && !st.videoOnly);
    st.seq = (st.seq || 0) + 1;
    const mdat = box("mdat", vData);
    const base = (samplesIn[0].dts != null ? samplesIn[0].dts : samplesIn[0].pts) - st.base;
    let moofBox = moof(st.seq, 1, base, vSamples, 0);
    moofBox = moof(st.seq, 1, base, vSamples, moofBox.length + 8);
    let hdr = concat([moofBox, mdat]);
    if (useAudio) {
      const freq = st.audio.freq || 44100;
      const aSamples = aFrames.map(function (f) {
        return { dur: 1024, size: f.data.length, cts: 0, flags: 0x02000000 };
      });
      const aData = concat(aFrames.map(function (f) { return f.data; }));
      st.aseq = (st.aseq || 0) + 1;
      const aBase = Math.round((aFrames[0].pts - st.base) * freq / TIMESCALE);
      let aMoof = moof(st.aseq, 2, aBase, aSamples, 0);
      aMoof = moof(st.aseq, 2, aBase, aSamples, aMoof.length + 8);
      hdr = concat([hdr, aMoof, box("mdat", aData)]);
    }
    let init = null;
    if (!st.inited) {
      if (useAudio) {
        codecs = codecs + ",mp4a.40.2";
        slog("moov", "hevc=" + hevc, w + "x" + h, codecs, "audio=" + st.audio.freq);
        init = concat([ftyp(hevc ? "hevc" : "avc"), moovAV(hevc, w, h, dec, st.audio)]);
      } else {
        slog("moov", "hevc=" + hevc, w + "x" + h, codecs);
        init = concat([ftyp(hevc ? "hevc" : "avc"), moovVideo(hevc, w, h, dec)]);
      }
      st.inited = true;
    }
    const firstDts = samplesIn[0].dts != null ? samplesIn[0].dts : samplesIn[0].pts;
    const dur = (lastDts - firstDts) / TIMESCALE;
    if (rawA.length && !useAudio && !st.videoOnly && !st.inited) slog("audio skip", "bytes=" + rawA.length);
    return { init: init, frag: hdr, duration: dur, state: st, codecs: codecs };
  }

  function parseMaster(text) {
    const lines = text.split(/\r?\n/);
    const out = [];
    for (let i = 0; i < lines.length; i++) {
      const l = lines[i];
      if (l.indexOf("#EXT-X-STREAM-INF:") !== 0) continue;
      const bw = Number((l.match(/BANDWIDTH=(\d+)/) || [])[1] || 0);
      const res = l.match(/RESOLUTION=(\d+)x(\d+)/);
      const codecs = ((l.match(/CODECS="([^"]+)"/) || [])[1] || "");
      const url = lines[++i] || "";
      out.push({ bandwidth: bw, width: res ? Number(res[1]) : 0, height: res ? Number(res[2]) : 0, codecs: codecs, url: url.trim() });
    }
    return out.sort(function (a, b) { return a.height - b.height; });
  }

  function parseMedia(text) {
    const lines = text.split(/\r?\n/);
    const segs = [];
    let dur = 4, seq = 0, total = 0;
    for (let i = 0; i < lines.length; i++) {
      const l = lines[i];
      if (l.indexOf("#EXT-X-MEDIA-SEQUENCE:") === 0) seq = Number(l.slice(22)) || 0;
      if (l.indexOf("#EXTINF:") === 0) dur = parseFloat(l.slice(8)) || dur;
      if (l && l[0] !== "#") {
        segs.push({ url: l.trim(), dur: dur });
        total += dur;
      }
    }
    return { segs: segs, seq: seq, duration: total };
  }

  function resolve(base, rel) {
    try { return new URL(rel, base).href; } catch (e) { return rel; }
  }

  function play(opts) {
    slog("play", opts.src);
    const video = opts.video;
    const fetchH = opts.headers || {};
    const ac = new AbortController();
    let levels = [];
    let current = null;
    let sb = null, ms = null;
    let st = {};
    let segs = [];
    let iSeg = 0;
    let appending = Promise.resolve();
    let pumpGen = 0;
    let fetchAc = new AbortController();
    let vodDur = 0;
    let segEpoch = 0;
    let pendingSeek = 0;
    let seekSeq = 0;
    const origPoster = video.getAttribute("poster") || "";
    let freezeOn = false;

    function freezeFrame() {
      try {
        if (!(video.videoWidth > 16) || video.readyState < 2) return;
        const c = document.createElement("canvas");
        const vw = video.videoWidth, vh = video.videoHeight;
        const w = vw > 640 ? 640 : vw;
        const h = Math.max(1, Math.round(vh * w / vw));
        c.width = w;
        c.height = h;
        c.getContext("2d").drawImage(video, 0, 0, w, h);
        video.poster = c.toDataURL("image/jpeg", 0.55);
        freezeOn = true;
      } catch (e) {}
    }
    function restorePoster() {
      if (!freezeOn) return;
      freezeOn = false;
      if (origPoster) video.poster = origPoster;
      else video.removeAttribute("poster");
    }
    video.addEventListener("playing", restorePoster);

    function segStartAt(idx) {
      let t = 0;
      for (let i = 0; i < idx && i < segs.length; i++) t += segs[i].dur || 0;
      return t;
    }
    function indexAtTime(t) {
      if (!(t > 0) || !segs.length) return 0;
      let acc = 0;
      for (let i = 0; i < segs.length; i++) {
        const d = segs[i].dur || 0;
        if (acc + d > t) return i;
        acc += d;
      }
      return Math.max(0, segs.length - 1);
    }

    let triedPlay = false;
    function kickPlay() {
      if (triedPlay) return;
      triedPlay = true;
      const p = video.play();
      if (p && p.catch) {
        p.catch(function (e) {
          if (e && e.name === "NotAllowedError") return;
          slog("play()", e && e.message);
        });
      }
    }

    function stale(gen) {
      return ac.signal.aborted || gen !== pumpGen;
    }

    async function get(url) {
      const r = await fetch(url, { headers: fetchH, signal: fetchAc.signal, cache: "no-store" });
      if (!r.ok) throw new Error("media " + r.status);
      return r;
    }

    function waitSB(target) {
      return new Promise(function (res, rej) {
        if (!target || !target.updating) return res();
        const ok = function () { target.removeEventListener("updateend", ok); target.removeEventListener("error", er); res(); };
        const er = function () {
          target.removeEventListener("updateend", ok);
          target.removeEventListener("error", er);
          const ve = video.error;
          rej(new Error("mse " + (ve && (ve.message || ve.code) || "sourcebuffer")));
        };
        target.addEventListener("updateend", ok);
        target.addEventListener("error", er);
      });
    }

    async function appendTo(target, buf, gen) {
      appending = appending.catch(function () {}).then(async function () {
        if (stale(gen) || !target) return;
        await waitSB(target);
        if (stale(gen) || !target) return;
        try {
          target.appendBuffer(buf);
        } catch (e) {
          slog("append throw", e && e.message, video.error && (video.error.message || video.error.code));
          throw e;
        }
        await waitSB(target);
      });
      return appending;
    }

    function timeCovered(t) {
      try {
        const b = video.buffered;
        for (let i = 0; i < b.length; i++) {
          if (t >= b.start(i) + 0.04 && t < b.end(i) - 0.12) return true;
        }
      } catch (e) {}
      return false;
    }
    function aheadEnd(t) {
      try {
        const b = video.buffered;
        for (let i = 0; i < b.length; i++) {
          if (t >= b.start(i) - 0.05 && t <= b.end(i) + 0.05) return b.end(i);
        }
      } catch (e) {}
      return -1;
    }
    function bumpPump() {
      if (opts.onAudioGate) opts.onAudioGate(true);
      freezeFrame();
      try { fetchAc.abort(); } catch (e) {}
      fetchAc = new AbortController();
      pumpGen++;
      appending = Promise.resolve();
      try { if (sb) sb.abort(); } catch (e) {}
      try {
        if (ms && video.src && video.src.indexOf("blob:") === 0) URL.revokeObjectURL(video.src);
      } catch (e) {}
      ms = null;
      sb = null;
      video.removeAttribute("src");
      video.load();
    }
    function aimAt(t) {
      st = {};
      st.mseReady = false;
      const at = t > 0 ? t : 0;
      iSeg = indexAtTime(at);
      segEpoch = segStartAt(iSeg);
      pendingSeek = at;
      if (at > 0.25) triedPlay = false;
      slog("aim", "t=" + at, "seg=" + iSeg, "epoch=" + segEpoch);
    }
    function runPump() {
      return pump().catch(function (e) {
        if (e && e.name === "AbortError") return;
        if (!ac.signal.aborted && opts.onError) opts.onError(e);
      });
    }

    async function openMSE(codecs, gen) {
      const mime = 'video/mp4; codecs="' + codecs + '"';
      const ok = !!(g.MediaSource && MediaSource.isTypeSupported(mime));
      slog("mse open", mime, "supported=" + ok);
      if (!ok) throw new Error("mse unsupported " + codecs);
      if (stale(gen)) return;
      const local = new MediaSource();
      const blob = URL.createObjectURL(local);
      video.src = blob;
      await new Promise(function (res, rej) {
        local.addEventListener("sourceopen", res, { once: true });
        local.addEventListener("error", function () { rej(new Error("mse source")); }, { once: true });
      });
      if (stale(gen)) {
        try { URL.revokeObjectURL(blob); } catch (e) {}
        return;
      }
      ms = local;
      sb = ms.addSourceBuffer(mime);
    }

    async function loadLevel(lv, resumeAt) {
      bumpPump();
      const gen = pumpGen;
      current = lv;
      const mediaUrl = resolve(opts.src, lv.url);
      let media;
      try {
        media = await (await get(mediaUrl)).text();
      } catch (e) {
        if (stale(gen) || (e && e.name === "AbortError")) return false;
        throw e;
      }
      if (stale(gen)) return false;
      const parsed = parseMedia(media);
      vodDur = parsed.duration || 0;
      if (opts.onDuration && vodDur > 0) opts.onDuration(vodDur);
      segs = parsed.segs.map(function (s) {
        return { url: resolve(mediaUrl, s.url), dur: s.dur };
      });
      const t = (typeof resumeAt === "number" && resumeAt > 0.25) ? resumeAt : 0;
      aimAt(t);
      slog("level", lv.height);
      if (opts.onLevels) opts.onLevels(levels);
      if (opts.onHeight) opts.onHeight(lv.height);
      return true;
    }

    async function pump() {
      const gen = pumpGen;
      while (!ac.signal.aborted && gen === pumpGen && iSeg < segs.length) {
        const idx = iSeg++;
        const seg = segs[idx];
        segEpoch = segStartAt(idx);
        let buf;
        try {
          buf = await (await get(seg.url)).arrayBuffer();
        } catch (e) {
          if (stale(gen) || (e && e.name === "AbortError")) return;
          throw e;
        }
        if (stale(gen)) return;
        slog("seg", (idx + 1) + "/" + segs.length, "epoch=" + segEpoch, "bytes=" + buf.byteLength);
        let out = remuxSegment(buf, st);
        async function feed(cur) {
          if (stale(gen)) return;
          st = cur.state;
          if (!st.mseReady && (cur.init || cur.frag)) {
            await openMSE(cur.codecs, gen);
            if (stale(gen) || !sb) return;
            st.mseReady = true;
          }
          if (cur.init) { slog("init", cur.init.length); await appendTo(sb, cur.init, gen); }
          if (stale(gen)) return;
          if (!st.tsOffApplied && sb) {
            try { sb.timestampOffset = segEpoch; } catch (e) {}
            st.tsOffApplied = true;
            slog("ts offset", segEpoch);
          }
          if (vodDur > 0 && ms && ms.readyState === "open" && !st.durSet) {
            try { ms.duration = vodDur; st.durSet = true; slog("mse duration", vodDur); } catch (e) { slog("mse duration", e && e.message); }
          }
          if (cur.frag) {
            slog("frag", cur.frag.length, "dur=" + cur.duration);
            await appendTo(sb, cur.frag, gen);
          }
          if (stale(gen)) return;
          if (pendingSeek > 0) {
            try { video.currentTime = pendingSeek; } catch (e) {}
            pendingSeek = 0;
            slog("seek", video.currentTime);
          }
          await new Promise(function (r) { setTimeout(r, 50); });
          if (stale(gen)) return;
          if (video.error) throw new Error("decode " + (video.error.message || video.error.code));
        }
        try {
          await feed(out);
        } catch (e) {
          if (stale(gen) || (e && e.name === "AbortError")) return;
          if (st.videoOnly) throw e;
          slog("retry video-only", e && e.message);
          freezeFrame();
          appending = Promise.resolve();
          try { if (sb) sb.abort(); } catch (x) {}
          try {
            if (ms && video.src && video.src.indexOf("blob:") === 0) URL.revokeObjectURL(video.src);
          } catch (x) {}
          ms = null;
          sb = null;
          video.removeAttribute("src");
          video.load();
          st = { videoOnly: true, sps: st.sps, pps: st.pps, vps: st.vps, hevc: st.hevc, w: st.w, h: st.h };
          if (stale(gen)) return;
          out = remuxSegment(buf, st);
          await feed(out);
        }
        if (stale(gen)) return;
        kickPlay();
        if (!out.init && !out.frag) throw new Error("empty remux " + seg.url);
        if (opts.onHeight && current) opts.onHeight(current.height);
        while (!ac.signal.aborted && gen === pumpGen && sb) {
          const t = (pendingSeek > 0 ? pendingSeek : (video.currentTime || 0));
          const end = aheadEnd(t);
          if (end < 0 || end - t < 12) break;
          await new Promise(function (r) { setTimeout(r, 400); });
        }
      }
      try { if (gen === pumpGen && ms && ms.readyState === "open" && iSeg >= segs.length) ms.endOfStream(); } catch (e) {}
    }

    const api = {
      levels: function () { return levels; },
      height: function () { return current && current.height; },
      seek: function (t) {
        if (typeof t !== "number" || !isFinite(t) || t < 0) return;
        if (vodDur > 0) t = Math.min(t, Math.max(0, vodDur - 0.08));
        if (opts.onAudioGate) opts.onAudioGate(true);
        seekSeq++;
        const seq = seekSeq;
        const at = t;
        if (timeCovered(at)) {
          pendingSeek = 0;
          setTimeout(function () {
            if (seq !== seekSeq) return;
            try { video.currentTime = at; } catch (e) {}
            slog("seek buffered", at);
          }, 50);
          return;
        }
        if (!segs.length) {
          try { video.currentTime = at; } catch (e) {}
          return;
        }
        setTimeout(function () {
          if (seq !== seekSeq) return;
          bumpPump();
          aimAt(at);
          runPump();
        }, 50);
      },
      setHeight: function (h) {
        const lv = levels.filter(function (x) { return x.height === h; })[0];
        if (!lv) return;
        if (current && current.height === h) return;
        const t = video.currentTime || 0;
        loadLevel(lv, t).then(function (ok) {
          if (ok === false) return;
          return pump();
        }).catch(function (e) {
          if (e && e.name === "AbortError") return;
          if (!ac.signal.aborted && opts.onError) opts.onError(e);
        });
      },
      destroy: function () {
        ac.abort();
        try { fetchAc.abort(); } catch (e) {}
        restorePoster();
        video.removeEventListener("playing", restorePoster);
        try { if (sb) sb.abort(); } catch (e) {}
        try { if (ms && video.src && video.src.indexOf("blob:") === 0) URL.revokeObjectURL(video.src); } catch (e) {}
      }
    };

    (async function () {
      const master = await (await get(opts.src)).text();
      levels = parseMaster(master);
      if (!levels.length) throw new Error("empty master");
      let pick = levels[0];
      const want = Number(opts.height) || 0;
      if (want > 0) {
        for (let i = 0; i < levels.length; i++) {
          if (levels[i].height <= want) pick = levels[i];
        }
      } else {
        pick = levels[Math.min(levels.length - 1, 2)] || levels[0];
      }
      await loadLevel(pick);
      await pump();
    })().catch(function (e) {
      if (e && e.name === "AbortError") return;
      if (!ac.signal.aborted && opts.onError) opts.onError(e);
    });

    return api;
  }

  g.mtTsFmp4 = { splitAnnexB: splitAnnexB, remuxSegment: remuxSegment, parseMaster: parseMaster, play: play };
})(typeof window !== "undefined" ? window : self);
