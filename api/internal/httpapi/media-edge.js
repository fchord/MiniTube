(function (w) {
  const PROBE_MS = 1600;
  let origin = w.location.origin;
  let ready = Promise.resolve();

  function privateHost(host) {
    const h = String(host || "").split(":")[0].toLowerCase();
    if (!h || h === "localhost" || h === "127.0.0.1" || h === "::1") return true;
    if (h.endsWith(".local")) return true;
    const p = h.split(".");
    if (p.length !== 4) return false;
    const a = Number(p[0]), b = Number(p[1]);
    if (a === 10) return true;
    if (a === 192 && b === 168) return true;
    if (a === 172 && b >= 16 && b <= 31) return true;
    return false;
  }

  function isStreamPath(p) {
    if (!p) return false;
    if (p.indexOf("/v1/live/") === 0) return true;
    if (p.indexOf("/v1/media/hls/") !== 0) return false;
    return /\.(m3u8|ts|vtt)$/i.test(p);
  }

  function rewrite(url) {
    if (!url) return url;
    try {
      const u = new URL(url, w.location.origin);
      if (!isStreamPath(u.pathname || "")) return url;
      const base = (origin || w.location.origin).replace(/\/$/, "");
      return base + u.pathname + u.search + u.hash;
    } catch (e) {
      return url;
    }
  }

  async function probe(edgeBase) {
    const ctrl = new AbortController();
    const timer = setTimeout(function () { ctrl.abort(); }, PROBE_MS);
    try {
      const r = await fetch(String(edgeBase).replace(/\/$/, "") + "/v1/media/edge-probe", {
        method: "GET",
        cache: "no-store",
        credentials: "omit",
        mode: "cors",
        signal: ctrl.signal
      });
      return r.status === 204 || r.ok;
    } catch (e) {
      return false;
    } finally {
      clearTimeout(timer);
    }
  }

  async function pick() {
    origin = w.location.origin;
    if (privateHost(w.location.hostname)) return;
    let site = {};
    try {
      const r = await fetch("/v1/public/site", { cache: "no-store", credentials: "same-origin" });
      site = await r.json();
    } catch (e) {
      return;
    }
    if (!site.mediaEdgeEnabled) return;
    const edges = Array.isArray(site.mediaEdges) ? site.mediaEdges : [];
    let force = "";
    try { force = new URLSearchParams(w.location.search).get("mediaEdge") || ""; } catch (e) {}
    let candidates = [];
    if (force) {
      candidates = edges.filter(function (e) { return e && e.id === force; });
    } else {
      const hint = String(site.mediaHint || "").toUpperCase();
      if (hint) {
        candidates = edges.filter(function (e) {
          const regions = (e && e.regions) || [];
          for (let i = 0; i < regions.length; i++) {
            if (String(regions[i]).toUpperCase() === hint) return true;
          }
          return false;
        });
      }
    }
    for (let i = 0; i < candidates.length; i++) {
      const e = candidates[i];
      if (e && e.base && await probe(e.base)) {
        origin = String(e.base).replace(/\/$/, "");
        return;
      }
    }
  }

  ready = pick();
  w.mtMedia = {
    rewrite: rewrite,
    ready: function () { return ready; },
    origin: function () { return origin; }
  };
})(window);
