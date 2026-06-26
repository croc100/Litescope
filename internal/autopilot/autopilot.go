// Package autopilot is the DBA self-driving moat: it derives a set of safe
// maintenance and optimization actions for a SQLite database, explains each in
// plain language, and applies the safe ones automatically (snapshotting first).
//
// Actions come from two sources:
//   - standard maintenance: ANALYZE, PRAGMA optimize, and VACUUM when the
//     database is meaningfully fragmented;
//   - the advisor: missing foreign-key indexes (auto-applied — additive and
//     high value) and redundant-index cleanup (proposed; applied only with
//     aggressive mode since dropping an index can regress a query).
//
// Every real apply is preceded by an automatic snapshot, so an autopilot run is
// always one `litescope restore` away from undo.
package autopilot

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/croc100/litescope/internal/advisor"
	"github.com/croc100/litescope/internal/health"
	"github.com/croc100/litescope/internal/snapshot"

	_ "modernc.org/sqlite"
)

// Risk classifies how cautious autopilot is about an action.
const (
	RiskSafe  = "safe"  // idempotent / additive — auto-applied
	RiskRisky = "risky" // could regress something — applied only in aggressive mode
)

// Action is one optimization step.
type Action struct {
	Kind   string `json:"kind"`            // analyze | optimize | vacuum | create-index | drop-index
	Risk   string `json:"risk"`            // safe | risky
	Table  string `json:"table,omitempty"`
	SQL    string `json:"sql"`             // the statement autopilot would run
	Reason string `json:"reason"`          // plain-language explanation
}

// ActionResult records what happened to an action during a run.
type ActionResult struct {
	Action
	Status string `json:"status"` // applied | proposed | skipped | failed
	Error  string `json:"error,omitempty"`
}

// Plan is the full set of actions autopilot proposes for a database.
type Plan struct {
	Path    string   `json:"path"`
	Actions []Action `json:"actions"`
}

// Result is the outcome of running a plan.
type Result struct {
	Path     string         `json:"path"`
	Applied  bool           `json:"applied"`
	Snapshot string         `json:"snapshot,omitempty"`
	Actions  []ActionResult `json:"actions"`
	Counts   map[string]int `json:"counts"` // status -> count
}

// BuildPlan inspects a database and returns the actions autopilot would take.
// queries (optional) are fed to the advisor for EXPLAIN QUERY PLAN analysis.
func BuildPlan(path string, queries []string) (*Plan, error) {
	p := &Plan{Path: path}

	// 1. Standard maintenance — always safe and cheap.
	p.Actions = append(p.Actions,
		Action{
			Kind: "analyze", Risk: RiskSafe, SQL: "ANALYZE;",
			Reason: "refresh query-planner statistics so SQLite picks better plans",
		},
		Action{
			Kind: "optimize", Risk: RiskSafe, SQL: "PRAGMA optimize;",
			Reason: "run SQLite's built-in optimizer (updates stale stats for active tables)",
		},
	)

	// 2. VACUUM only when fragmentation is worth the rewrite.
	h := health.Inspect(path, false)
	if h.SizeBytes >= health.FragmentationMinBytes && h.FragmentationPct() >= health.FragmentationRatio*100 {
		p.Actions = append(p.Actions, Action{
			Kind: "vacuum", Risk: RiskRisky, SQL: "VACUUM;",
			Reason: fmt.Sprintf("%.0f%% of the file is reclaimable free space; VACUUM compacts it (rewrites the whole database, takes a write lock)", h.FragmentationPct()),
		})
	}

	// 3. Advisor-derived actions.
	rep, err := advisor.Analyze(path, queries)
	if err != nil {
		return nil, err
	}
	for _, f := range rep.Findings {
		switch f.Rule {
		case "fk-no-index":
			p.Actions = append(p.Actions, Action{
				Kind: "create-index", Risk: RiskSafe, Table: f.Table, SQL: f.Suggestion,
				Reason: "foreign key has no index — every join or cascade scans the whole table; adding the index is additive and safe",
			})
		case "redundant-index":
			p.Actions = append(p.Actions, Action{
				Kind: "drop-index", Risk: RiskRisky, Table: f.Table, SQL: f.Suggestion,
				Reason: f.Detail + " — dropping it reclaims write overhead, but verify no query depends on it",
			})
		case "full-scan":
			// No runnable statement — surface as a proposal with guidance.
			p.Actions = append(p.Actions, Action{
				Kind: "create-index", Risk: RiskRisky, Table: f.Table, SQL: "",
				Reason: f.Detail + " — " + f.Suggestion,
			})
		}
	}

	return p, nil
}

// RunOptions controls how a plan is executed.
type RunOptions struct {
	Apply     bool // commit changes; false = dry-run (everything is "proposed")
	Aggressive bool // also apply RiskRisky actions
	NoSnapshot bool // skip the pre-run snapshot (not recommended)
}

// Run applies a plan according to opts. In dry-run mode nothing is executed and
// every action is reported as "proposed". When applying, a snapshot is taken
// first (unless disabled), then each runnable, in-policy action is executed.
func Run(path string, plan *Plan, opts RunOptions) (*Result, error) {
	res := &Result{Path: path, Counts: map[string]int{}}

	willApply := opts.Apply && hasRunnable(plan, opts.Aggressive)
	if willApply && !opts.NoSnapshot {
		snap, err := snapshot.Create(path, snapshot.CreateOptions{Label: "autopilot"})
		if err != nil {
			return nil, fmt.Errorf("pre-run snapshot failed — refusing to proceed: %w", err)
		}
		res.Snapshot = snap.Path
	}

	var db *sql.DB
	if opts.Apply {
		var err error
		db, err = sql.Open("sqlite", path)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", path, err)
		}
		defer db.Close()
	}

	for _, a := range plan.Actions {
		ar := ActionResult{Action: a}
		switch {
		case a.SQL == "":
			ar.Status = "proposed" // guidance only, nothing to run
		case !opts.Apply:
			ar.Status = "proposed"
		case a.Risk == RiskRisky && !opts.Aggressive:
			ar.Status = "skipped" // out of policy without --aggressive
		default:
			if _, err := db.Exec(a.SQL); err != nil {
				ar.Status = "failed"
				ar.Error = err.Error()
			} else {
				ar.Status = "applied"
				res.Applied = true
			}
		}
		res.Counts[ar.Status]++
		res.Actions = append(res.Actions, ar)
	}

	return res, nil
}

// hasRunnable reports whether any action would actually execute under apply.
func hasRunnable(plan *Plan, aggressive bool) bool {
	for _, a := range plan.Actions {
		if a.SQL == "" {
			continue
		}
		if a.Risk == RiskRisky && !aggressive {
			continue
		}
		return true
	}
	return false
}

// Summary renders a one-line plain-language summary of a result.
func (r *Result) Summary() string {
	if len(r.Actions) == 0 {
		return "nothing to do — database is already well-tuned"
	}
	var parts []string
	for _, status := range []string{"applied", "proposed", "skipped", "failed"} {
		if n := r.Counts[status]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, status))
		}
	}
	return strings.Join(parts, ", ")
}
