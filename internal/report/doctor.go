// Package report renders litescope results as standalone, shareable HTML —
// a single self-contained file (inline CSS, no assets) that can be attached to
// a PR, emailed, or committed as a build artifact.
package report

import (
	"html/template"
	"io"
	"time"

	"github.com/croc100/litescope/internal/advisor"
	"github.com/croc100/litescope/internal/health"
	"github.com/croc100/litescope/internal/lint"
)

// DoctorData is the input to the doctor HTML report. It mirrors the fields the
// CLI already computes so the report package stays decoupled from cobra.
type DoctorData struct {
	Path     string
	Verdict  string // healthy | attention | critical
	Health   *health.Report
	Advisor  *advisor.Report
	Lint     *lint.Report
	Warnings int
}

// finding is a unified view of an advisor or lint finding for the template.
type finding struct {
	Severity   string
	Table      string
	Rule       string
	Detail     string
	Suggestion string
}

type doctorView struct {
	DoctorData
	Generated      string
	VerdictLabel   string
	HealthLabel    string
	SizeHuman      string
	WALHuman       string
	FragPct        string
	WALBloated     bool
	FragHigh       bool
	AdvisorList    []finding
	LintList       []finding
}

// Doctor writes a standalone HTML report for a doctor run to w.
func Doctor(w io.Writer, d DoctorData) error {
	v := doctorView{
		DoctorData: d,
		Generated:  time.Now().Format("2006-01-02 15:04 MST"),
	}

	switch d.Verdict {
	case "critical":
		v.VerdictLabel = "Critical"
	case "attention":
		v.VerdictLabel = "Needs attention"
	default:
		v.VerdictLabel = "Healthy"
	}

	if h := d.Health; h != nil {
		v.HealthLabel = h.SeverityLabel
		v.SizeHuman = humanBytes(h.SizeBytes)
		v.WALHuman = humanBytes(h.WALBytes)
		v.FragPct = formatPct(h.FragmentationPct())
		v.WALBloated = h.WALBytes >= health.WALBloatBytes
		v.FragHigh = h.FragmentationPct() >= health.FragmentationRatio*100
	}
	if a := d.Advisor; a != nil {
		for _, f := range a.Findings {
			v.AdvisorList = append(v.AdvisorList, finding{
				Severity: f.Severity, Table: f.Table, Rule: f.Rule,
				Detail: f.Detail, Suggestion: f.Suggestion,
			})
		}
	}
	if l := d.Lint; l != nil {
		for _, f := range l.Findings {
			v.LintList = append(v.LintList, finding{
				Severity: string(f.Severity), Table: f.Table, Rule: f.Rule,
				Detail: f.Detail, Suggestion: f.Suggestion,
			})
		}
	}

	return doctorTmpl.Execute(w, v)
}

var doctorTmpl = template.Must(template.New("doctor").Parse(doctorHTML))

const doctorHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>litescope doctor — {{.Path}}</title>
<style>
  :root {
    --bg: #0d1117; --panel: #161b22; --border: #30363d;
    --fg: #e6edf3; --dim: #8b949e; --teal: #00d4aa;
    --ok: #3fb950; --warn: #d29922; --crit: #f85149;
  }
  * { box-sizing: border-box; }
  body { margin: 0; padding: 2.5rem 1rem; background: var(--bg); color: var(--fg);
    font: 15px/1.6 -apple-system, BlinkMacSystemFont, "Inter", "Segoe UI", sans-serif; }
  .wrap { max-width: 820px; margin: 0 auto; }
  .brand { color: var(--teal); font-weight: 700; letter-spacing: -0.02em; }
  h1 { font-size: 1.15rem; font-weight: 600; margin: 0 0 0.25rem; }
  .path { color: var(--dim); font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 0.9rem; word-break: break-all; }
  .verdict { display: inline-flex; align-items: center; gap: 0.5rem; margin: 1.25rem 0 2rem;
    padding: 0.5rem 1rem; border-radius: 999px; font-weight: 600; }
  .verdict .dot { width: 10px; height: 10px; border-radius: 50%; }
  .healthy { background: rgba(63,185,80,.12); color: var(--ok); }
  .healthy .dot { background: var(--ok); }
  .attention { background: rgba(210,153,34,.12); color: var(--warn); }
  .attention .dot { background: var(--warn); }
  .critical { background: rgba(248,81,73,.12); color: var(--crit); }
  .critical .dot { background: var(--crit); }
  section { background: var(--panel); border: 1px solid var(--border);
    border-radius: 10px; padding: 1.25rem 1.5rem; margin-bottom: 1.25rem; }
  section h2 { font-size: 0.8rem; text-transform: uppercase; letter-spacing: 0.06em;
    color: var(--dim); margin: 0 0 1rem; }
  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: 1rem; }
  .metric .k { color: var(--dim); font-size: 0.78rem; }
  .metric .v { font-size: 1.05rem; font-weight: 600; }
  .v.ok { color: var(--ok); } .v.warn { color: var(--warn); } .v.crit { color: var(--crit); }
  .f { padding: 0.85rem 0; border-top: 1px solid var(--border); }
  .f:first-of-type { border-top: none; }
  .f .head { display: flex; align-items: baseline; gap: 0.6rem; }
  .badge { font-size: 0.68rem; font-weight: 700; text-transform: uppercase;
    padding: 0.1rem 0.45rem; border-radius: 4px; }
  .badge.warning { background: rgba(210,153,34,.15); color: var(--warn); }
  .badge.info { background: rgba(139,148,158,.15); color: var(--dim); }
  .f .table { color: var(--teal); font-family: ui-monospace, monospace; font-size: 0.85rem; }
  .f .rule { color: var(--dim); font-size: 0.78rem; margin-left: auto; }
  .f .detail { margin: 0.35rem 0 0; }
  .f .sugg { margin: 0.4rem 0 0; padding: 0.4rem 0.6rem; background: rgba(0,212,170,.07);
    border-left: 2px solid var(--teal); border-radius: 4px;
    font-family: ui-monospace, monospace; font-size: 0.82rem; color: var(--fg); }
  .empty { color: var(--ok); }
  footer { color: var(--dim); font-size: 0.78rem; text-align: center; margin-top: 2rem; }
  footer a { color: var(--teal); text-decoration: none; }
</style>
</head>
<body>
<div class="wrap">
  <h1><span class="brand">litescope</span> doctor</h1>
  <div class="path">{{.Path}}</div>

  <div class="verdict {{.Verdict}}"><span class="dot"></span>{{.VerdictLabel}}</div>

  {{with .Health}}
  <section>
    <h2>Health</h2>
    <div class="grid">
      <div class="metric"><div class="k">Integrity</div>
        <div class="v {{if .IntegrityOK}}ok{{else}}crit{{end}}">{{if .IntegrityOK}}ok{{else}}FAILED{{end}}</div></div>
      <div class="metric"><div class="k">Size</div><div class="v">{{$.SizeHuman}}</div></div>
      <div class="metric"><div class="k">Journal</div><div class="v">{{if .JournalMode}}{{.JournalMode}}{{else}}—{{end}}</div></div>
      <div class="metric"><div class="k">WAL</div>
        <div class="v {{if $.WALBloated}}warn{{end}}">{{$.WALHuman}}</div></div>
      <div class="metric"><div class="k">Fragmentation</div>
        <div class="v {{if $.FragHigh}}warn{{end}}">{{$.FragPct}}</div></div>
    </div>
    {{if .Issues}}<div style="margin-top:1rem">
      {{range .Issues}}<div class="f"><div class="detail">→ {{.}}</div></div>{{end}}
    </div>{{end}}
  </section>
  {{end}}

  <section>
    <h2>Advisor · {{len .AdvisorList}} finding(s)</h2>
    {{if not .AdvisorList}}<div class="empty">No index or query problems found.</div>{{end}}
    {{range .AdvisorList}}
    <div class="f">
      <div class="head">
        <span class="badge {{.Severity}}">{{.Severity}}</span>
        {{if .Table}}<span class="table">{{.Table}}</span>{{end}}
        <span class="rule">{{.Rule}}</span>
      </div>
      <div class="detail">{{.Detail}}</div>
      {{if .Suggestion}}<div class="sugg">{{.Suggestion}}</div>{{end}}
    </div>
    {{end}}
  </section>

  <section>
    <h2>Lint · {{len .LintList}} finding(s)</h2>
    {{if not .LintList}}<div class="empty">No schema design problems found.</div>{{end}}
    {{range .LintList}}
    <div class="f">
      <div class="head">
        <span class="badge {{.Severity}}">{{.Severity}}</span>
        {{if .Table}}<span class="table">{{.Table}}</span>{{end}}
        <span class="rule">{{.Rule}}</span>
      </div>
      <div class="detail">{{.Detail}}</div>
      {{if .Suggestion}}<div class="sugg">{{.Suggestion}}</div>{{end}}
    </div>
    {{end}}
  </section>

  <footer>Generated {{.Generated}} by
    <a href="https://github.com/croc100/Litescope">litescope</a></footer>
</div>
</body>
</html>
`
