(function (w) {
  const TOKEN_KEY = "mt.accessToken";
  const COOKIE = "mt_access";
  function read(store) {
    try { return store.getItem(TOKEN_KEY) || ""; } catch (e) { return ""; }
  }
  function write(store, value) {
    try {
      if (value) store.setItem(TOKEN_KEY, value);
      else store.removeItem(TOKEN_KEY);
    } catch (e) {}
  }
  function syncCookie(t) {
    try {
      if (t) {
        document.cookie = COOKIE + "=" + encodeURIComponent(t) + "; Path=/; SameSite=Lax; Max-Age=604800";
      } else {
        document.cookie = COOKIE + "=; Path=/; Max-Age=0";
      }
    } catch (e) {}
  }
  function getToken() {
    const local = read(w.localStorage);
    if (local) {
      syncCookie(local);
      return local;
    }
    const sess = read(w.sessionStorage);
    if (sess) {
      write(w.localStorage, sess);
      write(w.sessionStorage, "");
      syncCookie(sess);
      return sess;
    }
    syncCookie("");
    return "";
  }
  function setToken(t) {
    write(w.localStorage, t);
    write(w.sessionStorage, "");
    syncCookie(t);
  }
  function clearToken() {
    write(w.localStorage, "");
    write(w.sessionStorage, "");
    syncCookie("");
  }
  function hlsConfig(extra) {
    extra = extra || {};
    extra.xhrSetup = function (xhr) {
      const t = getToken();
      if (t) xhr.setRequestHeader("Authorization", "Bearer " + t);
    };
    return extra;
  }
  w.mtAuth = { TOKEN_KEY: TOKEN_KEY, getToken: getToken, setToken: setToken, clearToken: clearToken, hlsConfig: hlsConfig };
  getToken();
})(window);
