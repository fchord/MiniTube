(function () {
  function esc(s) {
    return String(s ?? "").replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }

  window.renderPostImages = function (urls) {
    urls = (urls || []).filter(Boolean).slice(0, 9);
    if (!urls.length) return "";
    var n = urls.length;
    var kind = n === 1 ? "n1" : n === 2 ? "n2" : n === 3 ? "n3" : n <= 6 ? "n6" : "n9";
    var cells = urls.map(function (u, i) {
      return (
        '<button type="button" class="post-img" data-full="' +
        esc(u) +
        '" aria-label="查看图片 ' +
        (i + 1) +
        '"><img src="' +
        esc(u) +
        '" alt=""></button>'
      );
    }).join("");
    return '<div class="post-imgs ' + kind + '">' + cells + "</div>";
  };

  function lightbox() {
    var el = document.getElementById("lightbox");
    if (el) return el;
    el = document.createElement("div");
    el.id = "lightbox";
    el.hidden = true;
    el.setAttribute("role", "dialog");
    el.setAttribute("aria-modal", "true");
    el.setAttribute("aria-label", "查看图片");
    el.innerHTML = '<button type="button" class="lb-close" aria-label="关闭">×</button><img alt="">';
    document.body.appendChild(el);
    function close() {
      el.hidden = true;
      document.body.classList.remove("lb-open");
      el.querySelector("img").removeAttribute("src");
    }
    el.querySelector(".lb-close").addEventListener("click", function (e) {
      e.stopPropagation();
      close();
    });
    el.addEventListener("click", function (e) {
      if (e.target === el) close();
    });
    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape" && !el.hidden) close();
    });
    return el;
  }

  document.addEventListener("click", function (e) {
    var btn = e.target.closest(".post-img");
    if (!btn) return;
    e.preventDefault();
    e.stopPropagation();
    var src = btn.getAttribute("data-full");
    if (!src) return;
    var el = lightbox();
    el.querySelector("img").src = src;
    el.hidden = false;
    document.body.classList.add("lb-open");
    el.querySelector(".lb-close").focus();
  });
})();
