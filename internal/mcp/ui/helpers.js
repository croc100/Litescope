// Rendering helpers shared by the Litescope views. Kept apart from the bridge:
// that file is the protocol, this one is presentation.
(function () {
  "use strict";
  var esc = window.litescope.escape;

  // sevClass maps every verdict word litescope emits onto the three status
  // colours. Tools disagree on wording (ok/warning/critical for health,
  // ok/attention/critical for locks), so normalise here rather than per view.
  function sevClass(s) {
    switch (String(s || "").toLowerCase()) {
      case "critical":
      case "error":
      case "locked":
        return "crit";
      case "warning":
      case "attention":
      case "warn":
        return "warn";
      default:
        return "ok";
    }
  }

  function badge(label, sev) {
    var cls = sevClass(sev === undefined ? label : sev);
    return '<span class="badge ' + cls + '"><span class="dot"></span>' + esc(label) + "</span>";
  }

  function bytes(n) {
    n = Number(n) || 0;
    if (n < 1024) return n + " B";
    var units = ["KB", "MB", "GB", "TB"];
    var i = -1;
    do {
      n /= 1024;
      i++;
    } while (n >= 1024 && i < units.length - 1);
    return (n < 10 ? n.toFixed(1) : Math.round(n)) + " " + units[i];
  }

  function num(n) {
    return Number(n || 0).toLocaleString();
  }

  function metric(k, v, small) {
    return (
      '<div class="metric"><div class="k">' + esc(k) + '</div>' +
      '<div class="v' + (small ? " sm" : "") + '">' + v + "</div></div>"
    );
  }

  function metrics(items) {
    var html = items
      .filter(function (m) {
        return m;
      })
      .map(function (m) {
        return metric(m[0], m[1], m[2]);
      })
      .join("");
    return '<div class="metrics">' + html + "</div>";
  }

  function header(title, source, right) {
    return (
      '<div class="hdr"><h1>' + esc(title) + "</h1>" +
      (source ? '<span class="src">' + esc(source) + "</span>" : "") +
      (right || "") +
      "</div>"
    );
  }

  // shortSource trims a long DSN to something that fits a header line without
  // losing the part that identifies the database.
  function shortSource(s) {
    s = String(s || "");
    if (s.length <= 48) return s;
    return "…" + s.slice(-47);
  }

  function render(html) {
    document.getElementById("root").innerHTML = html;
    window.litescope.resize();
  }

  // wire binds click handlers by data-act, so views build HTML as strings and
  // still get behaviour without inline handlers (which the host's CSP blocks).
  function wire(map) {
    Array.prototype.forEach.call(document.querySelectorAll("[data-act]"), function (el) {
      var fn = map[el.getAttribute("data-act")];
      if (fn) el.addEventListener("click", function () { fn(el); });
    });
  }

  // busy shows progress on a button while an async action runs.
  function busy(el, label, promise) {
    var prev = el.textContent;
    el.disabled = true;
    el.textContent = label;
    return promise.catch(function (err) {
      window.litescope.fail(err);
    }).then(function (v) {
      el.disabled = false;
      el.textContent = prev;
      return v;
    });
  }

  window.ui = {
    esc: esc, sevClass: sevClass, badge: badge, bytes: bytes, num: num,
    metrics: metrics, header: header, shortSource: shortSource, render: render,
    wire: wire, busy: busy,
  };
})();
