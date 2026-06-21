/* Litescope site i18n — shared by / and /docs.
 * Element-level translation keyed by normalized English innerHTML.
 * Untranslated elements gracefully fall back to English.
 * Code blocks, flag tables, terminals and command names stay English by design. */
(function () {
  "use strict";

  var LANGS = [["en", "ENG"], ["ko", "KOR"], ["zh", "CHN"], ["ja", "JPN"]];
  var STORE = "ls_lang";

  // Elements eligible for translation. Leaf-filtered at runtime.
  var SEL = [
    ".nav-links a", '[style*="gap:24px"] a',
    ".badge", ".eyebrow", ".hero-sub", ".section-sub",
    "h1", "h2", "h3",
    ".card p", ".stack-item div", ".spot-list li>div", ".step p",
    ".plan-tier", ".plan-price", ".plan-desc", ".plan-features li",
    ".compare td", ".compare th", ".btn",
    ".foot-note",
    ".sidebar-section",
    ".doc-section>p", ".callout", "#gui ul li", "#advise ul li"
  ].join(",");

  var SKIP = ".terminal, .flag-table, .install-box, pre, #changelog, #lang-switcher";
  var MARK_ONLY = /^[\s✓✗—\-·\d]*$|^Unlimited$/;

  function norm(s) { return (s || "").replace(/\s+/g, " ").trim(); }

  var DICT = window.LS_I18N || {};

  var records = []; // { el, en, icon, rest }

  // Split off a leading decorative icon span (e.g. the ✓ bullet or pulse dot)
  // so dictionary keys can match the meaningful text alone.
  function splitIcon(el) {
    var first = el.firstElementChild;
    if (first && first.matches && first.matches("span.check, span.pulse")
      && norm(first.textContent).length <= 1) {
      var iconHTML = first.outerHTML;
      var html = el.innerHTML;
      var idx = html.indexOf(iconHTML);
      if (idx === 0) return { icon: iconHTML, rest: html.slice(iconHTML.length) };
    }
    return { icon: "", rest: el.innerHTML };
  }

  function collect() {
    var els = document.querySelectorAll(SEL);
    for (var i = 0; i < els.length; i++) {
      var el = els[i];
      if (el.closest(SKIP)) continue;
      if (el.querySelector(SEL)) continue;            // not a leaf block
      var txt = norm(el.textContent);
      if (!txt || txt.length < 2) continue;
      if (MARK_ONLY.test(el.textContent.trim())) continue;
      var s = splitIcon(el);
      records.push({ el: el, en: el.innerHTML, icon: s.icon, rest: s.rest });
    }
  }

  function apply(lang) {
    var d = DICT[lang] || {};
    for (var i = 0; i < records.length; i++) {
      var r = records[i];
      if (lang === "en") { r.el.innerHTML = r.en; continue; }
      var tr = d[norm(r.rest)];
      r.el.innerHTML = r.icon + (tr != null ? tr : r.rest);
    }
    document.documentElement.lang = lang;
  }

  // ---- Language switcher UI (matches the pill + dropdown mockup) ----
  function injectStyles() {
    var css = ""
      + "#lang-switcher{position:relative;font-family:inherit;display:inline-flex;}"
      + "#lang-switcher .ls-btn{display:inline-flex;align-items:center;gap:6px;cursor:pointer;"
      + "background:rgba(255,255,255,0.04);border:1px solid rgba(255,255,255,0.12);color:inherit;"
      + "font:inherit;font-size:13px;font-weight:600;letter-spacing:.04em;padding:6px 11px;border-radius:9px;line-height:1;}"
      + "#lang-switcher .ls-btn:hover{border-color:rgba(0,212,170,0.5);}"
      + "#lang-switcher .ls-caret{transition:transform .18s;font-size:10px;opacity:.8;}"
      + "#lang-switcher.open .ls-caret{transform:rotate(180deg);}"
      + "#lang-switcher .ls-menu{position:absolute;top:calc(100% + 8px);right:0;min-width:150px;"
      + "background:#0e1320;border:1px solid rgba(255,255,255,0.12);border-radius:12px;padding:8px;"
      + "box-shadow:0 18px 50px -16px rgba(0,0,0,0.7);display:none;z-index:1000;}"
      + "#lang-switcher.open .ls-menu{display:block;}"
      + "#lang-switcher .ls-item{display:block;width:100%;text-align:left;cursor:pointer;"
      + "background:transparent;border:0;color:#8b95a6;font:inherit;font-size:14px;font-weight:600;"
      + "letter-spacing:.04em;padding:10px 12px;border-radius:8px;}"
      + "#lang-switcher .ls-item:hover{color:#e6edf3;background:rgba(255,255,255,0.05);}"
      + "#lang-switcher .ls-item.active{color:#e6edf3;background:rgba(255,255,255,0.07);}";
    var s = document.createElement("style");
    s.textContent = css;
    document.head.appendChild(s);
  }

  function labelFor(code) {
    for (var i = 0; i < LANGS.length; i++) if (LANGS[i][0] === code) return LANGS[i][1];
    return "ENG";
  }

  function buildSwitcher(current) {
    var host = document.createElement("div");
    host.id = "lang-switcher";

    var btn = document.createElement("button");
    btn.className = "ls-btn";
    btn.type = "button";
    btn.innerHTML = '<span class="ls-cur">' + labelFor(current) + '</span><span class="ls-caret">▾</span>';

    var menu = document.createElement("div");
    menu.className = "ls-menu";
    LANGS.forEach(function (l) {
      var item = document.createElement("button");
      item.type = "button";
      item.className = "ls-item" + (l[0] === current ? " active" : "");
      item.textContent = l[1];
      item.addEventListener("click", function (e) {
        e.stopPropagation();
        setLang(l[0]);
        host.classList.remove("open");
      });
      menu.appendChild(item);
    });

    btn.addEventListener("click", function (e) {
      e.stopPropagation();
      host.classList.toggle("open");
    });
    document.addEventListener("click", function () { host.classList.remove("open"); });

    host.appendChild(btn);
    host.appendChild(menu);
    return host;
  }

  function setLang(code) {
    try { localStorage.setItem(STORE, code); } catch (e) {}
    apply(code);
    var sw = document.getElementById("lang-switcher");
    if (sw) {
      var cur = sw.querySelector(".ls-cur");
      if (cur) cur.textContent = labelFor(code);
      var items = sw.querySelectorAll(".ls-item");
      items.forEach(function (it) {
        it.classList.toggle("active", it.textContent === labelFor(code));
      });
    }
  }

  function init() {
    var saved = "en";
    try { saved = localStorage.getItem(STORE) || "en"; } catch (e) {}
    if (!labelFor(saved)) saved = "en";

    collect();
    injectStyles();

    // Place the switcher next to the "Docs" link (works on landing + docs nav),
    // else fall back to a fixed top-right pill.
    var anchor = document.querySelector('.nav-links a[href="/docs"]')
      || document.querySelector('[style*="gap:24px"] a[href="/docs"]');
    var sw = buildSwitcher(saved);
    if (anchor && anchor.parentElement) {
      anchor.parentElement.appendChild(sw);
    } else {
      sw.style.position = "fixed";
      sw.style.top = "14px";
      sw.style.right = "18px";
      sw.style.zIndex = "1000";
      document.body.appendChild(sw);
    }

    if (saved !== "en") apply(saved);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
