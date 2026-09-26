// Fleet view — every database in the fleet, worst first. The point a
// per-database tool cannot make: which of the hundred files needs attention
// now, and how much of the fleet is affected.
(function () {
  "use strict";
  var ls = window.litescope;

  function row(r) {
    var rep = r.report || {};
    var sev = rep.severity || (rep.reachable === false ? "critical" : "ok");
    var issues = rep.issues || [];
    return (
      "<tr>" +
      '<td><span class="name">' + ui.esc(r.database || r.name || "") + "</span></td>" +
      "<td>" + ui.badge(sev) + "</td>" +
      '<td class="num">' + ui.bytes(rep.size_bytes) + "</td>" +
      '<td class="num">' + ui.bytes(rep.wal_bytes) + "</td>" +
      "<td>" + (issues.length ? ui.esc(issues[0]) + (issues.length > 1 ? " +" + (issues.length - 1) : "") : '<span style="opacity:.5">—</span>') + "</td>" +
      '<td style="text-align:right"><button data-act="inspect" data-db="' + ui.esc(r.dsn || r.database || "") + '">Inspect</button></td>' +
      "</tr>"
    );
  }

  ls.onInput(function () {});
  ls.onResult(function (d) {
    if (!d) return ls.fail(new Error("The fleet report could not be read."));
    var results = d.results || [];
    var counts = { ok: 0, warn: 0, crit: 0 };
    results.forEach(function (r) {
      counts[ui.sevClass((r.report || {}).severity)]++;
    });

    // Worst-first: the tool already sorts, but a client that re-serialises the
    // array should not change what the operator sees at the top.
    results = results.slice().sort(function (a, b) {
      var rank = { crit: 0, warn: 1, ok: 2 };
      return rank[ui.sevClass((a.report || {}).severity)] - rank[ui.sevClass((b.report || {}).severity)];
    });

    ui.render(
      ui.header(
        "Fleet health",
        results.length + (results.length === 1 ? " database" : " databases"),
        ui.badge(counts.crit ? "critical" : counts.warn ? "attention" : "ok")
      ) +
      ui.metrics([
        ["critical", '<span class="crit">' + ui.num(counts.crit) + "</span>"],
        ["attention", '<span class="warn">' + ui.num(counts.warn) + "</span>"],
        ["healthy", '<span class="ok">' + ui.num(counts.ok) + "</span>"],
        d.checked_at ? ["checked", ui.esc(String(d.checked_at).replace("T", " ").slice(0, 19)) + " UTC", true] : null,
      ]) +
      (results.length
        ? '<table><thead><tr><th>Database</th><th>Status</th><th style="text-align:right">Size</th>' +
          '<th style="text-align:right">WAL</th><th>Top issue</th><th></th></tr></thead><tbody>' +
          results.map(row).join("") +
          "</tbody></table>"
        : '<div class="empty">The fleet config lists no databases.</div>') +
      '<div class="actions"><button data-act="refresh">Refresh</button>' +
      (counts.crit ? '<button data-act="triage">Triage the failing databases</button>' : "") +
      "</div>"
    );

    ui.wire({
      refresh: function (el) { ui.busy(el, "Checking…", ls.refresh()); },
      inspect: function (el) { ls.say("Show me the health and lock state of " + el.getAttribute("data-db")); },
      triage: function () {
        var bad = results
          .filter(function (r) { return ui.sevClass((r.report || {}).severity) === "crit"; })
          .map(function (r) { return r.dsn || r.database; });
        ls.say("These fleet databases are critical — diagnose each one and propose a fix:\n" + bad.join("\n"));
      },
    });
  });
})();
