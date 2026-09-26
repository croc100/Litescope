// Lock doctor view — the diagnosis for "database is locked", which is the
// single most common way a production SQLite database fails. The static
// report explains why contention is possible at all; the live probe answers
// "is something holding the lock right now?".
(function () {
  "use strict";
  var ls = window.litescope;

  function findings(list) {
    if (!list || !list.length) {
      return '<div class="finding ok"><h2>No lock-configuration problems found</h2>' +
        '<p>Journal mode, busy timeout and locking mode are all set so concurrent readers and writers can make progress.</p></div>';
    }
    return list
      .map(function (f) {
        var sev = ui.sevClass(f.Severity || f.severity);
        var fix = f.Fix || f.fix;
        return (
          '<div class="finding ' + sev + '">' +
          "<h2>" + ui.esc(f.Summary || f.summary) +
          '<span class="rule">' + ui.esc(f.Rule || f.rule || "") + "</span></h2>" +
          "<p>" + ui.esc(f.Detail || f.detail || "") + "</p>" +
          (fix ? '<pre class="fix">' + ui.esc(fix) + "</pre>" : "") +
          "</div>"
        );
      })
      .join("");
  }

  function holders(list) {
    if (!list || !list.length) return '<div class="empty">No process is holding the file open.</div>';
    return (
      '<table><thead><tr><th>Process</th><th>PID</th><th style="text-align:right">Access</th></tr></thead><tbody>' +
      list
        .map(function (h) {
          return (
            "<tr><td><span class=\"name\">" + ui.esc(h.command || h.Command || h.name || "?") + "</span></td>" +
            '<td class="num">' + ui.esc(h.pid || h.PID || "") + "</td>" +
            '<td class="num">' + ui.esc(h.access || h.Access || h.mode || "") + "</td></tr>"
          );
        })
        .join("") +
      "</tbody></table>"
    );
  }

  function renderLive(d, source) {
    var state = d.state || d.State || "unknown";
    ui.render(
      ui.header("Lock state — live probe", ui.shortSource(source), ui.badge(state, state === "free" || state === "readable" ? "ok" : state)) +
      ui.metrics([
        ["state", ui.esc(state), true],
        ["holders", ui.num((d.holders || d.Holders || []).length)],
        d.waited_ms !== undefined ? ["wait", ui.esc(d.waited_ms) + " ms"] : null,
      ]) +
      '<div class="sect">Processes with the file open</div>' +
      holders(d.holders || d.Holders) +
      '<div class="actions">' +
      '<button data-act="static">Show configuration diagnosis</button>' +
      '<button data-act="again">Probe again</button>' +
      "</div>"
    );
    ui.wire({
      static: function (el) { ui.busy(el, "Loading…", ls.refresh({ live: false })); },
      again: function (el) { ui.busy(el, "Probing…", ls.refresh({ live: true })); },
    });
  }

  function renderStatic(d, source) {
    var verdict = d.Verdict || d.verdict || "ok";
    var p = d.Pragmas || d.pragmas || {};
    var list = d.Findings || d.findings || [];
    var wal = d.WALBytes || d.wal_bytes || 0;

    ui.render(
      ui.header("Lock doctor", ui.shortSource(source), ui.badge(verdict)) +
      ui.metrics([
        ["journal", ui.esc(p.journal_mode || "—"), true],
        ["busy timeout", ui.esc(p.busy_timeout !== undefined ? p.busy_timeout + " ms" : "0 ms"), true],
        ["locking", ui.esc(p.locking_mode || "—"), true],
        ["wal", ui.bytes(wal), true],
        ["findings", ui.num(list.length)],
      ]) +
      findings(list) +
      '<div class="actions">' +
      '<button data-act="live">Probe live lock state</button>' +
      (list.length ? '<button data-act="apply">Ask the agent to apply these fixes</button>' : "") +
      "</div>" +
      (d.Provider && d.Provider !== "local"
        ? '<div class="note">Provider <b>' + ui.esc(d.Provider) + "</b>: contention is managed by the platform, so the findings above are guidance rather than PRAGMAs you can set.</div>"
        : "")
    );

    ui.wire({
      live: function (el) { ui.busy(el, "Probing…", ls.refresh({ live: true })); },
      // The view never writes. Handing the fix back to the conversation keeps
      // the agent — and the user's approval of it — in the loop, which is the
      // whole point of running writes through litescope.
      apply: function () {
        var fixes = list
          .map(function (f) { return "- " + (f.Rule || f.rule) + ": " + (f.Fix || f.fix || "").split("\n")[0]; })
          .join("\n");
        ls.say("Apply the lock doctor fixes to " + source + ":\n" + fixes);
      },
    });
  }

  ls.onInput(function () {});
  ls.onResult(function (d) {
    if (!d) return ls.fail(new Error("The lock report could not be read."));
    var source = d.Source || d.source || (ls.args && ls.args.source) || "";
    if (d.state !== undefined || d.State !== undefined || d.holders !== undefined) renderLive(d, source);
    else renderStatic(d, source);
  });
})();
