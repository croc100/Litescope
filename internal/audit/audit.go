// Package audit records an append-only, local-first history of every operation
// that changes a database — migrations, fleet convergence/recovery, SQL writes,
// and inline row edits. Each line is one JSON object in ~/.litescope/audit.log,
// giving an "ops layer" the provenance it needs: who changed what, when, and how
// it turned out. There is no server; the log lives on the operator's machine.
package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

// Entry is one recorded operation.
type Entry struct {
	Time     time.Time `json:"time"`
	Operator string    `json:"operator"`
	Action   string    `json:"action"`  // e.g. "migrate.apply", "fleet.converge", "sql.write"
	Target   string    `json:"target"`  // database path or fleet name
	Summary  string    `json:"summary"` // human-readable what-happened
	Outcome  string    `json:"outcome"` // "ok" | "error"
	Detail   string    `json:"detail,omitempty"`
}

// LogPath returns the audit log location. LITESCOPE_AUDIT_LOG overrides it
// (used by tests); otherwise it is ~/.litescope/audit.log.
func LogPath() string {
	if p := os.Getenv("LITESCOPE_AUDIT_LOG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".litescope-audit.log"
	}
	return filepath.Join(home, ".litescope", "audit.log")
}

// Operator resolves who is performing operations: LITESCOPE_OPERATOR if set,
// otherwise the OS username, otherwise "unknown".
func Operator() string {
	if v := strings.TrimSpace(os.Getenv("LITESCOPE_OPERATOR")); v != "" {
		return v
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "unknown"
}

// Record appends an entry to the audit log. It fills in Time and Operator when
// unset and is best-effort: a logging failure never blocks the operation, it is
// returned so the caller may surface it if it cares.
func Record(e Entry) error {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if e.Operator == "" {
		e.Operator = Operator()
	}
	if e.Outcome == "" {
		e.Outcome = "ok"
	}
	path := LogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

// Read returns up to limit recent entries (newest first), optionally filtered by
// target substring and exact action. A missing log is not an error: it returns
// an empty slice.
func Read(limit int, target, action string) ([]Entry, error) {
	f, err := os.Open(LogPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var all []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if json.Unmarshal(line, &e) != nil {
			continue // skip a corrupt line rather than fail the whole read
		}
		if target != "" && !strings.Contains(e.Target, target) {
			continue
		}
		if action != "" && e.Action != action {
			continue
		}
		all = append(all, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	// newest first
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}
