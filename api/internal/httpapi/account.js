(function () {
  const auth = window.mtAuth || {
    TOKEN_KEY: "mt.accessToken",
    getToken: function () { return ""; },
    setToken: function () {},
    clearToken: function () {}
  };
  const TOKEN_KEY = auth.TOKEN_KEY;

  function isHan(ch) {
    const c = ch.codePointAt(0);
    return (c >= 0x3400 && c <= 0x9FFF) || (c >= 0xF900 && c <= 0xFAFF) || (c >= 0x20000 && c <= 0x2CEAF);
  }
  function isLatinLetter(ch) {
    return /^[A-Za-z\u00C0-\u024F]$/.test(ch);
  }
  function nameInitials(name) {
    const chars = [...String(name || "").trim()].filter(c => !/\s/.test(c));
    if (!chars.length) return "?";
    if (isHan(chars[0])) return chars[0];
    if (isLatinLetter(chars[0])) {
      const letters = chars.filter(isLatinLetter);
      if (letters.length >= 2) return (letters[0] + letters[1]).toUpperCase();
      return letters[0].toUpperCase();
    }
    return chars[0];
  }
  function fillFace(el, me) {
    el.textContent = "";
    if (me && me.avatarUrl) {
      const img = document.createElement("img");
      img.alt = "";
      img.src = me.avatarUrl;
      el.appendChild(img);
      return;
    }
    el.textContent = nameInitials(me && (me.nickname || me.username));
  }

  function ensureSlot() {
    const header = document.querySelector("header");
    if (!header) return null;
    let nav = header.querySelector(":scope > nav");
    if (!nav) {
      nav = document.createElement("nav");
      nav.className = "mt-nav";
      while (header.firstChild) nav.appendChild(header.firstChild);
      header.appendChild(nav);
    }
    let btn = document.getElementById("accountBtn");
    if (!btn) {
      btn = document.createElement("button");
      btn.type = "button";
      btn.id = "accountBtn";
      btn.className = "account is-pending";
      btn.setAttribute("aria-busy", "true");
      header.appendChild(btn);
    }
    let menu = document.getElementById("accountMenu");
    if (!menu) {
      menu = document.createElement("div");
      menu.id = "accountMenu";
      menu.className = "account-menu";
      menu.hidden = true;
      menu.innerHTML =
        '<div class="account-menu-user">' +
          '<span class="account-menu-avatar" id="accountMenuFace"></span>' +
          '<span class="account-menu-name" id="accountMenuName"></span>' +
        "</div>" +
        '<a class="account-menu-item" id="accountChannel" href="#">我的频道</a>' +
        '<a class="account-menu-item" href="/settings">设置</a>' +
        '<button type="button" class="account-menu-item" id="accountLogout">退出账号</button>';
      document.body.appendChild(menu);
    }
    return { btn, menu };
  }

  function placeMenu(btn, menu) {
    const r = btn.getBoundingClientRect();
    menu.style.top = r.bottom + "px";
    menu.style.right = (document.documentElement.clientWidth - r.right) + "px";
    menu.style.left = "auto";
  }

  function boot() {
    const slot = ensureSlot();
    if (!slot) return;
    const { btn, menu } = slot;
    let me = null;

    function closeMenu() {
      menu.hidden = true;
      btn.setAttribute("aria-expanded", "false");
    }
    function openMenu() {
      if (!me) return;
      fillFace(document.getElementById("accountMenuFace"), me);
      document.getElementById("accountMenuName").textContent = "@" + me.username;
      const handle = me.defaultChannel && me.defaultChannel.handle;
      const ch = document.getElementById("accountChannel");
      ch.href = handle ? "/c/" + encodeURIComponent(handle) : "/u/" + encodeURIComponent(me.username);
      menu.hidden = false;
      btn.setAttribute("aria-expanded", "true");
      placeMenu(btn, menu);
    }

    function showGuest() {
      me = null;
      closeMenu();
      btn.classList.remove("is-pending");
      btn.removeAttribute("aria-busy");
      btn.dataset.mode = "guest";
      btn.setAttribute("aria-label", "登录");
      btn.removeAttribute("aria-expanded");
      btn.textContent = "登录";
    }
    function showUser(user) {
      me = user;
      btn.classList.remove("is-pending");
      btn.removeAttribute("aria-busy");
      btn.dataset.mode = "user";
      btn.setAttribute("aria-label", "账号菜单");
      btn.setAttribute("aria-expanded", "false");
      fillFace(btn, user);
    }

    btn.addEventListener("click", () => {
      if (btn.dataset.mode === "guest") {
        location.href = "/login";
        return;
      }
      if (btn.dataset.mode !== "user") return;
      if (menu.hidden) openMenu();
      else closeMenu();
    });
    document.getElementById("accountLogout").addEventListener("click", async () => {
      const tok = auth.getToken();
      if (tok) {
        await fetch("/v1/auth/logout", {
          method: "POST",
          headers: { Authorization: "Bearer " + tok }
        }).catch(() => {});
      }
      auth.clearToken();
      showGuest();
    });
    document.addEventListener("click", (e) => {
      if (menu.hidden) return;
      if (menu.contains(e.target) || btn.contains(e.target)) return;
      closeMenu();
    });
    window.addEventListener("resize", () => { if (!menu.hidden) placeMenu(btn, menu); });
    window.addEventListener("scroll", () => { if (!menu.hidden) placeMenu(btn, menu); }, true);

    async function paint(opts) {
      const tok = auth.getToken();
      if (!tok) {
        showGuest();
        return;
      }
      const r = await fetch("/v1/me", { headers: { Authorization: "Bearer " + tok } });
      if (!r.ok) {
        auth.clearToken();
        showGuest();
        return;
      }
      const user = await r.json();
      if (opts && opts.bustAvatar && user.avatarUrl) {
        user.avatarUrl += (user.avatarUrl.indexOf("?") >= 0 ? "&" : "?") + "t=" + Date.now();
      }
      showUser(user);
    }
    paint().catch(() => {
      if (btn.classList.contains("is-pending")) showGuest();
    });
    window.addEventListener("minitube:account-changed", () => {
      paint({ bustAvatar: true }).catch(() => {});
    });
    window.addEventListener("storage", (e) => {
      if (e.key && e.key !== TOKEN_KEY) return;
      paint().catch(() => {});
    });
  }

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", boot);
  else boot();
})();
