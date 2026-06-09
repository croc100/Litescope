package render

import (
	"html/template"
	"io"

	"github.com/croc100/litescope/internal/diff"
)

var htmlTmpl = template.Must(template.New("report").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<title>Litescope Report</title>
<style>
  body { font-family: monospace; padding: 2rem; background: #0d1117; color: #c9d1d9; }
  h2 { color: #58a6ff; border-bottom: 1px solid #30363d; padding-bottom: .4rem; }
  table { border-collapse: collapse; width: 100%; margin-bottom: 2rem; }
  th { text-align: left; color: #8b949e; font-weight: normal; padding: .3rem .8rem; }
  td { padding: .3rem .8rem; border-top: 1px solid #21262d; }
  .add { color: #3fb950; }
  .del { color: #f85149; }
  .chg { color: #d29922; }
</style>
</head>
<body>
<h1>Litescope Diff Report</h1>

{{if .Schema}}
<h2>Schema diff</h2>
<table>
<tr><th>Status</th><th>Table</th><th>Detail</th></tr>
{{range .Schema}}
{{if .Added}}<tr><td class="add">+</td><td>{{.Name}}</td><td class="add">new table ({{len .AddedColumns}} columns)</td></tr>
{{else if .Removed}}<tr><td class="del">-</td><td>{{.Name}}</td><td class="del">table removed</td></tr>
{{else}}<tr><td class="chg">~</td><td>{{.Name}}</td><td>
  {{range .AddedColumns}}<span class="add">+ column {{.Name}} ({{.Type}})</span><br>{{end}}
  {{range .RemovedColumns}}<span class="del">- column {{.Name}}</span><br>{{end}}
  {{range .ChangedColumns}}<span class="chg">~ column {{.Name}} ({{.Old.Type}} → {{.New.Type}})</span><br>{{end}}
  {{range .AddedIndexes}}<span class="add">+ index {{.Name}}</span><br>{{end}}
  {{range .RemovedIndexes}}<span class="del">- index {{.Name}}</span><br>{{end}}
</td></tr>
{{end}}
{{end}}
</table>
{{end}}

{{if .Data}}
<h2>Data diff</h2>
<table>
<tr><th>Table</th><th>Added</th><th>Removed</th><th>Changed</th></tr>
{{range .Data}}
<tr>
  <td>{{.Table}}</td>
  <td class="add">{{if .Added}}+{{.Added}}{{end}}</td>
  <td class="del">{{if .Removed}}-{{.Removed}}{{end}}</td>
  <td class="chg">{{if .Changed}}~{{.Changed}}{{end}}</td>
</tr>
{{end}}
</table>
{{end}}

</body>
</html>
`))

func HTML(w io.Writer, r *diff.Result) error {
	return htmlTmpl.Execute(w, r)
}
