// Health view — the pre-flight read an agent should take before it touches a
// database: is the file intact, is the WAL checkpointing, is anything holding
// space, and is there a backup to fall back to.
(function () {
  "use strict";
  var ls = window.litescope;

  function issues(list) {
    if (!list || !list.length) {
      return '<div class="finding ok"><h2>No operational faults detected</h2>' +
        "<p>Integrity check passed and no WAL, fragmentation or reachability problem was found.</p></div>";
    }
    return list
      .map(function (i) {
        return '<div class="finding warn"><h2>' + ui.esc(i) + "</h2></div>";
      })
      .join("");
  }

  ls.onInput(function () {});
  ls.onResult(function (d) {
    if (!d) return ls.fail(new Error("The health report could not be read."));
    var source = d.path || d.source || (ls.args && ls.args.source) || "";
    var sev = d.severity || "ok";
    var list = d.issues || [];
    var pages = Number(d.page_count || 0);
    var free = Number(d.freelist_count || 0);
    var frag = pages > 0 ? Math.round((free / pages) * 100) : 0;

    ui.render(
      ui.header("Database health", ui.shortSource(source), ui.badge(sev)) +
      ui.metrics([
        ["size", ui.bytes(d.size_bytes)],
        ["pages", ui.num(pages)],
        ["wal", ui.bytes(d.wal_bytes)],
        ["free pages", ui.num(free) + (frag ? ' <span style="opacity:.6">' + frag + "%</span>" : "")],
        ["journal", ui.esc(d.journal_mode || "—"), true],
        ["integrity", d.integrity_ok === false ? '<span class="crit">failed</span>' : "passed", true],
        ["snapshots", ui.num(d.snapshot_count)],
      ]) +
      issues(list) +
      (d.has_backup === false
        ? '<div class="note">No snapshot exists for this database. Take one before any write you might need to undo: <b>litescope snapshot ' + ui.esc(ui.shortSource(source)) + "</b></div>"
        : "") +
      (d.note ? '<div class="note">' + ui.esc(d.note) + "</div>" : "") +
      '<div class="actions">' +
      '<button data-act="refresh">Refresh</button>' +
      '<button data-act="deep">Deep integrity check</button>' +
      '<button data-act="locks">Check lock contention</button>' +
      "</div>"
    );

    ui.wire({
      refresh: function (el) { ui.busy(el, "Checking…", ls.refresh()); },
      // The exhaustive integrity_check reads every page, so it is opt-in
      // rather than what the first render runs.
      deep: function (el) { ui.busy(el, "Scanning…", ls.refresh({ deep: true })); },
      locks: function () { ls.say("Run the lock doctor on " + source); },
    });
  });
})();
