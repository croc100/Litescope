// Package metrics renders fleet health and schema-fingerprint state as
// Prometheus / OpenMetrics text exposition, so a Litescope fleet drops straight
// into Grafana / Alertmanager without a bespoke integration.
package metrics

import (
	"fmt"
	"strings"

	"github.com/croc100/litescope/internal/fleet"
)

// Render returns the Prometheus text exposition for a fleet's health and
// (optional) schema fingerprint. fp may be nil to skip schema metrics.
func Render(fr *fleet.HealthReport, fp *fleet.FingerprintReport) string {
	var b strings.Builder

	// Map database name -> fingerprint cluster id + drifted flag.
	type fpInfo struct {
		cluster string
		drifted bool
	}
	driftByName := map[string]fpInfo{}
	if fp != nil {
		for _, c := range fp.Clusters {
			for _, m := range c.Members {
				driftByName[m] = fpInfo{cluster: c.ID, drifted: !c.IsCanonical}
			}
		}
	}

	// --- Per-database gauges ---
	gauge(&b, "litescope_database_reachable",
		"Whether the database is reachable (1) or not (0).")
	for _, r := range fr.Results {
		b.WriteString(metricLine("litescope_database_reachable",
			dbLabels(r), boolVal(r.Report != nil && r.Report.Reachable)))
	}

	gauge(&b, "litescope_database_integrity_ok",
		"Whether the database passed its integrity check (1) or not (0).")
	for _, r := range fr.Results {
		b.WriteString(metricLine("litescope_database_integrity_ok",
			dbLabels(r), boolVal(r.Report != nil && r.Report.IntegrityOK)))
	}

	gauge(&b, "litescope_database_severity",
		"Health severity: 0=ok, 1=warning, 2=critical.")
	for _, r := range fr.Results {
		sev := 0
		if r.Report != nil {
			sev = int(r.Report.Severity)
		}
		b.WriteString(metricLine("litescope_database_severity",
			dbLabels(r), fmt.Sprintf("%d", sev)))
	}

	gauge(&b, "litescope_database_size_bytes", "Main database file size in bytes.")
	for _, r := range fr.Results {
		if r.Report != nil {
			b.WriteString(metricLine("litescope_database_size_bytes",
				dbLabels(r), fmt.Sprintf("%d", r.Report.SizeBytes)))
		}
	}

	gauge(&b, "litescope_database_wal_bytes", "Write-ahead log (-wal) size in bytes.")
	for _, r := range fr.Results {
		if r.Report != nil {
			b.WriteString(metricLine("litescope_database_wal_bytes",
				dbLabels(r), fmt.Sprintf("%d", r.Report.WALBytes)))
		}
	}

	gauge(&b, "litescope_database_freelist_pages",
		"Freelist page count (reclaimable space; a VACUUM candidate).")
	for _, r := range fr.Results {
		if r.Report != nil {
			b.WriteString(metricLine("litescope_database_freelist_pages",
				dbLabels(r), fmt.Sprintf("%d", r.Report.FreelistCount)))
		}
	}

	if fp != nil {
		gauge(&b, "litescope_database_drifted",
			"Whether the database's schema drifts from canonical (1) or not (0).")
		for _, r := range fr.Results {
			info, ok := driftByName[r.Database]
			b.WriteString(metricLine("litescope_database_drifted",
				dbLabels(r), boolVal(ok && info.drifted)))
		}
	}

	// --- Fleet aggregates ---
	ok, warning, critical := fr.Counts()
	gauge(&b, "litescope_fleet_databases", "Number of databases by severity.")
	b.WriteString(metricLine("litescope_fleet_databases", `severity="ok"`, fmt.Sprintf("%d", ok)))
	b.WriteString(metricLine("litescope_fleet_databases", `severity="warning"`, fmt.Sprintf("%d", warning)))
	b.WriteString(metricLine("litescope_fleet_databases", `severity="critical"`, fmt.Sprintf("%d", critical)))

	gauge(&b, "litescope_fleet_databases_total", "Total databases inspected.")
	b.WriteString(metricLine("litescope_fleet_databases_total", "", fmt.Sprintf("%d", len(fr.Results))))

	if fp != nil {
		gauge(&b, "litescope_fleet_schema_clusters",
			"Number of distinct schema clusters across the fleet (1 = no drift).")
		b.WriteString(metricLine("litescope_fleet_schema_clusters", "", fmt.Sprintf("%d", len(fp.Clusters))))

		drifted := 0
		for _, c := range fp.Clusters {
			if !c.IsCanonical {
				drifted += c.Count
			}
		}
		gauge(&b, "litescope_fleet_drifted_databases_total",
			"Number of databases whose schema drifts from canonical.")
		b.WriteString(metricLine("litescope_fleet_drifted_databases_total", "", fmt.Sprintf("%d", drifted)))

		if len(fp.Unreachable) > 0 {
			gauge(&b, "litescope_fleet_unreachable_total",
				"Number of databases that could not be fingerprinted.")
			b.WriteString(metricLine("litescope_fleet_unreachable_total", "", fmt.Sprintf("%d", len(fp.Unreachable))))
		}
	}

	return b.String()
}

// dbLabels builds the label set for one database result.
func dbLabels(r fleet.HealthResult) string {
	labels := []string{fmt.Sprintf(`database=%q`, escapeLabel(r.Database))}
	if r.Report != nil && r.Report.Remote {
		labels = append(labels, `remote="true"`)
	}
	return strings.Join(labels, ",")
}

func gauge(b *strings.Builder, name, help string) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
}

func metricLine(name, labels, value string) string {
	if labels == "" {
		return fmt.Sprintf("%s %s\n", name, value)
	}
	return fmt.Sprintf("%s{%s} %s\n", name, labels, value)
}

func boolVal(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// escapeLabel escapes a Prometheus label value per the text exposition spec.
func escapeLabel(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}
