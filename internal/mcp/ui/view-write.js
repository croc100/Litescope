// Blast-radius view — what a write would do, before it does it. This is the
// screen the reversible-write contract exists for: rows affected, the exact
// statements, and the token that undoes the write once it is applied.
(function () {
  "use strict";
  var ls = window.litescope;

  function statements(list) {
    if (!list || !list.length) return "";
    return (
      '<div class="sect">Statements</div>' +
      '<table><thead><tr><th>SQL</th><th>Kind</th><th style="text-align:right">Rows</th></tr></thead><tbody>' +
      list
        .map(function (s) {
          return (
            '<tr><td><span class="name">' + ui.esc(s.sql) + "</span></td>" +
            "<td>" + ui.esc(s.kind || "") + "</td>" +
            '<td class="num">' + ui.num(s.rows_affected) + "</td></tr>"
          );
        })
        .join("") +
      "</tbody></table>"
    );
  }

  // schemaChanges renders the structural half of the blast radius: a write
  // that alters the schema is far more consequential than one that does not,
  // so it is called out separately from the row counts.
  function schemaChanges(diff) {
    if (!diff) return "";
    var rows = [];
    (diff.Schema || diff.schema || []).forEach(function (c) {
      rows.push([c.table || c.Table || "", c.change || c.Change || JSON.stringify(c)]);
    });
    (diff.Data || diff.data || []).forEach(function (c) {
      rows.push([c.table || c.Table || "", (c.delta !== undefined ? (c.delta > 0 ? "+" : "") + c.delta + " rows" : JSON.stringify(c))]);
    });
    if (!rows.length) return "";
    return (
      '<div class="sect">Blast radius</div>' +
      "<table><tbody>" +
      rows
        .map(function (r) {
          return '<tr><td><span class="name">' + ui.esc(r[0]) + "</span></td><td>" + ui.esc(r[1]) + "</td></tr>";
        })
        .join("") +
      "</tbody></table>"
    );
  }

  ls.onInput(function () {});
  ls.onResult(function (d) {
    if (!d) return ls.fail(new Error("The write preview could not be read."));

    // A lock failure comes back as lock-doctor remediation instead of a
    // result; show it as the blocking problem it is.
    if (d.ok === false || d.error) {
      ui.render(
        ui.header("Write blocked", "", ui.badge("blocked", "critical")) +
        '<div class="finding crit"><h2>' + ui.esc(d.error || "The write did not run") + "</h2>" +
        (d.remediation ? "<p>" + ui.esc(d.remediation) + "</p>" : "") +
        "</div>"
      );
      return;
    }

    var applied = d.applied === true;
    var source = d.source || (ls.args && ls.args.source) || "";
    var rows = Number(d.rows_affected || 0);
    var token = d.rewind_token || d.rewind || "";

    ui.render(
      ui.header(
        applied ? "Write applied" : "Write preview — dry run",
        ui.shortSource(source),
        ui.badge(applied ? "applied" : "not applied", applied ? "warning" : "ok")
      ) +
      ui.metrics([
        ["rows affected", ui.num(rows)],
        ["statements", ui.num(d.statements)],
        ["provider", ui.esc(d.provider || "local"), true],
        token ? ["undo point", "captured", true] : null,
      ]) +
      statements(d.preview || d.statements_detail) +
      schemaChanges(d.blast_radius_diff) +
      (token
        ? '<div class="sect">Rewind token</div><pre class="fix">' + ui.esc(token) + "</pre>"
        : "") +
      (d.note ? '<div class="note">' + ui.esc(d.note) + "</div>" : "") +
      '<div class="actions">' +
      (applied
        ? '<button class="danger" data-act="undo">Undo this write</button>'
        : '<button data-act="apply">Apply this write</button>') +
      "</div>"
    );

    ui.wire({
      // Applying and undoing both go back through the conversation rather than
      // firing tools/call from the view. A click inside an embedded panel is
      // not the same thing as a user approving a production write, and this
      // tool exists precisely to keep that boundary explicit.
      apply: function () {
        ls.say(
          "Apply this write to " + source + " (rows affected: " + rows + "):\n" +
          (ls.args && ls.args.sql ? ls.args.sql : "")
        );
      },
      undo: function () {
        ls.say("Undo the write on " + source + " using rewind token " + token);
      },
    });
  });
})();
