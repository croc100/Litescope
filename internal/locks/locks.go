// Package locks diagnoses SQLite locking and writer-starvation issues.
// It inspects PRAGMA settings for local databases and provides provider-specific
// guidance for D1 and Turso where PRAGMAs are not directly accessible.
package locks

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

// Severity levels for lock findings.
const (
	SeverityOK       = "ok"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// Finding is one diagnosed locking issue with a concrete prescription.
type Finding struct {
	Severity string // "ok" | "warning" | "critical"
	Rule     string // machine-readable rule ID
	Summary  string // one-line description of the problem
	Detail   string // explanation of why this matters
	Fix      string // exact PRAGMA/config to apply (empty if n/a)
}

// Report is the full output of a lock diagnosis.
type Report struct {
	Source   string
	Provider string            // "local" | "d1" | "turso"
	Pragmas  map[string]string // PRAGMA values (local only)
	Findings []Finding
	Verdict  string // "ok" | "attention" | "critical"
	WALBytes int64  // WAL file size in bytes (local only)
}

// Diagnose inspects a database source and returns a lock health report.
// For local files it reads PRAGMAs directly; for remote sources it returns
// provider-specific guidance without a live connection.
func Diagnose(source string) (*Report, error) {
	if strings.HasPrefix(source, "d1://") {
		return diagnoseD1(source), nil
	}
	if strings.HasPrefix(source, "turso://") {
		return diagnoseTurso(source), nil
	}
	return diagnoseLocal(source)
}

// ── local SQLite ──────────────────────────────────────────────────────────────

func diagnoseLocal(path string) (*Report, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("cannot open %s: %w", path, err)
	}

	db, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer db.Close()

	pragmas, err := readPragmas(db, []string{
		"journal_mode",
		"wal_autocheckpoint",
		"locking_mode",
		"synchronous",
	})
	if err != nil {
		return nil, fmt.Errorf("reading PRAGMAs: %w", err)
	}

	walBytes := walFileSize(path)

	var findings []Finding

	// Rule: journal mode should be WAL
	jm := strings.ToUpper(pragmas["journal_mode"])
	if jm != "WAL" {
		findings = append(findings, Finding{
			Severity: SeverityCritical,
			Rule:     "journal-not-wal",
			Summary:  fmt.Sprintf("journal_mode=%s — every write blocks all readers", pragmas["journal_mode"]),
			Detail: "In DELETE/TRUNCATE/PERSIST journal modes SQLite holds an exclusive write lock " +
				"for the entire duration of a transaction. Any concurrent reader or writer " +
				"sees SQLITE_BUSY immediately. WAL mode allows one writer and many readers to " +
				"proceed in parallel.",
			Fix: "PRAGMA journal_mode=WAL;",
		})
	} else {
		findings = append(findings, Finding{
			Severity: SeverityOK,
			Rule:     "journal-not-wal",
			Summary:  "journal_mode=WAL — concurrent reads allowed during writes",
		})
	}

	// Rule: busy_timeout defaults to 0, causing immediate SQLITE_BUSY on any lock contention
	findings = append(findings, Finding{
		Severity: SeverityCritical,
		Rule:     "busy-timeout-zero",
		Summary:  "busy_timeout defaults to 0 — any lock contention returns SQLITE_BUSY immediately",
		Detail: "Every new connection starts with busy_timeout=0, which means any lock contention " +
			"returns SQLITE_BUSY / \"database is locked\" immediately. This causes application " +
			"crashes under concurrent load. Set busy_timeout in your connection pool or DSN " +
			"so every connection retries instead of failing.",
		Fix: "// DSN: file:app.db?_busy_timeout=5000\n// Or after open: PRAGMA busy_timeout=5000;",
	})

	// Rule: exclusive locking mode prevents multi-process access
	lm := strings.ToUpper(pragmas["locking_mode"])
	if lm == "EXCLUSIVE" {
		findings = append(findings, Finding{
			Severity: SeverityWarning,
			Rule:     "locking-mode-exclusive",
			Summary:  "locking_mode=EXCLUSIVE — this connection holds the database exclusively",
			Detail: "EXCLUSIVE locking mode keeps a file lock for the lifetime of the connection, " +
				"preventing all other processes from opening the database. This is sometimes " +
				"intentional for single-writer setups but will cause SQLITE_BUSY for every " +
				"other process.",
			Fix: "PRAGMA locking_mode=NORMAL;",
		})
	}

	// Rule: large WAL file signals checkpoint starvation
	if walBytes > 100*1024*1024 { // 100 MB
		findings = append(findings, Finding{
			Severity: SeverityWarning,
			Rule:     "wal-bloat",
			Summary:  fmt.Sprintf("WAL file is %s — checkpoint may be stalled", formatBytes(walBytes)),
			Detail: "The WAL file grows until a checkpoint is triggered (default: every 1000 pages). " +
				"A large WAL slows reads (SQLite must scan the WAL for every read) and can " +
				"starve checkpoints if a long-running reader holds an open snapshot.",
			Fix: "PRAGMA wal_checkpoint(TRUNCATE);",
		})
	} else if walBytes > 0 {
		findings = append(findings, Finding{
			Severity: SeverityOK,
			Rule:     "wal-bloat",
			Summary:  fmt.Sprintf("WAL file is %s — within normal range", formatBytes(walBytes)),
		})
	}

	verdict := verdictFromFindings(findings)

	return &Report{
		Source:   path,
		Provider: "local",
		Pragmas:  pragmas,
		Findings: findings,
		Verdict:  verdict,
		WALBytes: walBytes,
	}, nil
}

// ── D1 ────────────────────────────────────────────────────────────────────────

func diagnoseD1(source string) *Report {
	findings := []Finding{
		{
			Severity: SeverityOK,
			Rule:     "d1-wal-internal",
			Summary:  "D1 uses WAL mode internally — concurrent reads never block writers",
			Detail:   "Cloudflare D1 manages SQLite's journal mode internally. You cannot set PRAGMA journal_mode on D1.",
		},
		{
			Severity: SeverityWarning,
			Rule:     "d1-no-busy-timeout",
			Summary:  "busy_timeout is not configurable on D1 — contention surfaces as HTTP errors",
			Detail: "D1 serializes writes at the HTTP API layer. When writes conflict you receive " +
				"HTTP 429 (rate limit) or 503 responses rather than SQLITE_BUSY. " +
				"Your Worker must handle retries.",
			Fix: "// In your Worker:\nconst result = await env.DB.prepare(sql).run();\n// Wrap in retry logic for 429/503 responses.",
		},
		{
			Severity: SeverityWarning,
			Rule:     "d1-concurrent-writes",
			Summary:  "D1 serializes writes — multiple concurrent Workers queue behind each other",
			Detail: "D1's HTTP API processes one write at a time per database. High-concurrency " +
				"write workloads will see increased latency as Workers queue. Use batch() to " +
				"consolidate multiple writes into a single round-trip.",
			Fix: "// Prefer D1 batch() for multiple writes:\nawait env.DB.batch([stmt1, stmt2, stmt3]);",
		},
		{
			Severity: SeverityWarning,
			Rule:     "d1-implicit-transactions",
			Summary:  "Long Worker execution can hold implicit D1 transactions",
			Detail: "Each D1 query runs in an implicit transaction. If your Worker does significant " +
				"work between D1 calls, other Workers writing to the same database will queue " +
				"behind it. Keep D1 operations grouped and close to each other in execution time.",
		},
	}

	return &Report{
		Source:   source,
		Provider: "d1",
		Findings: findings,
		Verdict:  "attention",
	}
}

// ── Turso ─────────────────────────────────────────────────────────────────────

func diagnoseTurso(source string) *Report {
	findings := []Finding{
		{
			Severity: SeverityOK,
			Rule:     "turso-wal-internal",
			Summary:  "Turso uses libSQL with WAL mode — concurrent reads are non-blocking",
		},
		{
			Severity: SeverityWarning,
			Rule:     "turso-single-writer",
			Summary:  "Turso databases are single-writer — concurrent writes are serialized",
			Detail: "Turso (libSQL) enforces one writer at a time per database. Multiple concurrent " +
				"writers will queue. For write-heavy workloads consider per-tenant databases " +
				"(one Turso DB per tenant) to spread write load.",
		},
		{
			Severity: SeverityWarning,
			Rule:     "turso-busy-timeout",
			Summary:  "Set busy_timeout in your Turso client connection string",
			Detail:   "Turso supports busy_timeout via the connection URL parameter.",
			Fix:      "// Connection URL: turso://TOKEN@ORG/DB?busy_timeout=5000",
		},
	}

	return &Report{
		Source:   source,
		Provider: "turso",
		Findings: findings,
		Verdict:  "attention",
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func readPragmas(db *sql.DB, names []string) (map[string]string, error) {
	result := make(map[string]string, len(names))
	for _, name := range names {
		var val string
		row := db.QueryRow("PRAGMA " + name)
		if err := row.Scan(&val); err != nil {
			val = ""
		}
		result[name] = val
	}
	return result, nil
}

func walFileSize(dbPath string) int64 {
	info, err := os.Stat(dbPath + "-wal")
	if err != nil {
		return 0
	}
	return info.Size()
}

func verdictFromFindings(findings []Finding) string {
	verdict := SeverityOK
	for _, f := range findings {
		switch f.Severity {
		case SeverityCritical:
			return "critical"
		case SeverityWarning:
			verdict = "attention"
		}
	}
	return verdict
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
