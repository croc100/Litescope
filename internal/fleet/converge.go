package fleet

import (
	"fmt"
	"time"

	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/migrate"
	"github.com/croc100/litescope/internal/schema"
)

// ConvergeClusterPlan is the convergence work for one drifted schema cluster:
// the SQL that transforms every member into the canonical schema.
type ConvergeClusterPlan struct {
	ClusterID   string           `json:"cluster_id"`
	Members     []Database       `json:"-"`        // full entries so we have DSNs
	MemberNames []string         `json:"members"`  // names, for JSON/display
	SQL         string           `json:"sql"`
	Statements  int              `json:"statements"`
	Destructive bool             `json:"destructive"` // drops a column/table on these DBs
	Drift       []diff.TableDiff `json:"drift,omitempty"`
}

// ConvergePlan describes what it takes to bring every drifted database in the
// fleet back to the canonical schema.
type ConvergePlan struct {
	CanonicalID     string                `json:"canonical_id"`
	AlreadyOK       int                   `json:"already_ok"`        // databases already matching canonical
	Clusters        []ConvergeClusterPlan `json:"clusters"`          // drifted clusters needing convergence
	TotalToConverge int                   `json:"total_to_converge"` // total drifted databases
	Unreachable     []FingerprintError    `json:"unreachable,omitempty"`
}

// HasDestructive reports whether converging any cluster would drop data.
func (p *ConvergePlan) HasDestructive() bool {
	for _, c := range p.Clusters {
		if c.Destructive {
			return true
		}
	}
	return false
}

// PlanConvergence fingerprints the fleet and, for every cluster that differs
// from canonical, generates the migration SQL to converge it. When canonical is
// nil, the largest cluster's schema is used as the reference.
func PlanConvergence(dbs []Database, canonical *schema.Schema, concurrency int) (*ConvergePlan, error) {
	report := Fingerprint(dbs, concurrency)
	if canonical == nil {
		if len(report.Clusters) == 0 {
			return nil, fmt.Errorf("no readable databases to converge")
		}
		canonical = report.Clusters[0].schema
	}
	canonHash := Hash(canonical)

	byName := make(map[string]Database, len(dbs))
	for _, db := range dbs {
		byName[db.Name] = db
	}

	plan := &ConvergePlan{
		CanonicalID: canonHash[:8],
		Unreachable: report.Unreachable,
	}

	for _, c := range report.Clusters {
		if Hash(c.schema) == canonHash {
			plan.AlreadyOK += c.Count
			continue
		}
		// SQL direction: transform a member (old) INTO canonical (new).
		toCanonical := diff.CompareSchemas(c.schema, canonical)
		m := migrate.Generate(toCanonical, canonical)

		// Display direction: how the cluster differs FROM canonical, matching
		// fingerprint semantics (canonical=old, cluster=new) so "+ extra" /
		// "- missing" labels read correctly. Reversing the SQL diff would
		// invert these labels.
		fromCanonical := diff.CompareSchemas(canonical, c.schema)

		members := make([]Database, 0, len(c.Members))
		for _, name := range c.Members {
			members = append(members, byName[name])
		}

		plan.Clusters = append(plan.Clusters, ConvergeClusterPlan{
			ClusterID:   c.ID,
			Members:     members,
			MemberNames: c.Members,
			SQL:         m.SQL(),
			Statements:  len(m.Statements),
			Destructive: m.HasWarnings(),
			Drift:       fromCanonical.Schema,
		})
		plan.TotalToConverge += len(members)
	}

	return plan, nil
}

// Converge applies a convergence plan, staged and fail-closed: databases are
// migrated one at a time across all clusters, and the first failure halts the
// rollout so a bad convergence can't cascade. Reuses the same per-database
// safety pipeline as Rollout (backup, transaction, verification, restore).
//
// In dry-run mode every database is validated (apply + rollback) and the run
// never halts early, so you see every database that would fail at once.
func Converge(plan *ConvergePlan, opts RolloutOptions) *RolloutReport {
	report := &RolloutReport{StartedAt: time.Now().UTC(), DryRun: opts.DryRun}
	halted := false
	applied := 0

	for _, cp := range plan.Clusters {
		stmts := migrate.SplitStatements(cp.SQL)
		for _, db := range cp.Members {
			res := RolloutResult{Database: db.Name, DSN: db.DSN, Provider: providerOf(db.DSN)}

			if halted {
				res.State = StateSkipped
				report.Results = append(report.Results, res)
				continue
			}
			if opts.Canary > 0 && applied >= opts.Canary && !opts.DryRun {
				res.State = StateCanary
				report.Results = append(report.Results, res)
				continue
			}
			if len(stmts) == 0 {
				res.State = StateFailed
				res.Err = fmt.Errorf("convergence produced no executable statements for cluster %s", cp.ClusterID)
				res.Error = res.Err.Error()
				report.Results = append(report.Results, res)
				if !opts.DryRun {
					halted = true
					report.Halted = true
				}
				continue
			}

			applyOne(&res, db, cp.SQL, stmts, opts)
			report.Results = append(report.Results, res)

			switch res.State {
			case StateApplied, StateDryRun:
				applied++
			case StateFailed:
				if !opts.DryRun {
					halted = true
					report.Halted = true
				}
			}
		}
	}

	return report
}
