package validate

import (
	"fmt"

	"github.com/croc100/litescope/internal/diff"
)

// ChangeKind describes what kind of change was observed.
type ChangeKind string

const (
	KindTableAdded      ChangeKind = "table added"
	KindTableRemoved    ChangeKind = "table removed"
	KindColumnAdded     ChangeKind = "column added"
	KindColumnRemoved   ChangeKind = "column removed"
	KindColumnChanged   ChangeKind = "column type changed"
	KindIndexAdded      ChangeKind = "index added"
	KindIndexRemoved    ChangeKind = "index removed"
	KindDataRowsChanged ChangeKind = "row count out of range"
)

// Change is a single atomic schema or data change.
type Change struct {
	Kind    ChangeKind
	Table   string
	Subject string // column/index name, or empty for table-level changes
	Detail  string // e.g. "TEXT→VARCHAR"
}

func (c Change) String() string {
	if c.Subject != "" {
		return fmt.Sprintf("%s.%s: %s %s", c.Table, c.Subject, c.Kind, c.Detail)
	}
	return fmt.Sprintf("%s: %s", c.Table, c.Kind)
}

// Result is the outcome of a validation run.
type Result struct {
	Passed     bool
	Confirmed  []Change // expected and found
	Unexpected []Change // found but not expected
	Missing    []Change // expected but not found
}

// Validate compares a diff result against a spec and returns a Result.
func Validate(d *diff.Result, spec *Spec) *Result {
	actual := collectChanges(d)
	expected := specToChanges(spec)

	confirmed := []Change{}
	unexpected := []Change{}
	missing := []Change{}

	// Mark which expected changes were found
	matched := make([]bool, len(expected))

	for _, a := range actual {
		found := false
		for i, e := range expected {
			if !matched[i] && changesEqual(a, e) {
				matched[i] = true
				confirmed = append(confirmed, a)
				found = true
				break
			}
		}
		if !found {
			unexpected = append(unexpected, a)
		}
	}

	for i, e := range expected {
		if !matched[i] {
			missing = append(missing, e)
		}
	}

	return &Result{
		Passed:     len(unexpected) == 0 && len(missing) == 0,
		Confirmed:  confirmed,
		Unexpected: unexpected,
		Missing:    missing,
	}
}

func changesEqual(a, b Change) bool {
	return a.Kind == b.Kind && a.Table == b.Table && a.Subject == b.Subject
}

// collectChanges flattens a diff.Result into a list of Changes.
func collectChanges(d *diff.Result) []Change {
	var out []Change

	for _, td := range d.Schema {
		if td.Added {
			out = append(out, Change{Kind: KindTableAdded, Table: td.Name})
			continue
		}
		if td.Removed {
			out = append(out, Change{Kind: KindTableRemoved, Table: td.Name})
			continue
		}
		for _, c := range td.AddedColumns {
			out = append(out, Change{Kind: KindColumnAdded, Table: td.Name, Subject: c.Name})
		}
		for _, c := range td.RemovedColumns {
			out = append(out, Change{Kind: KindColumnRemoved, Table: td.Name, Subject: c.Name})
		}
		for _, c := range td.ChangedColumns {
			out = append(out, Change{
				Kind:    KindColumnChanged,
				Table:   td.Name,
				Subject: c.Name,
				Detail:  fmt.Sprintf("%s→%s", c.Old.Type, c.New.Type),
			})
		}
		for _, ix := range td.AddedIndexes {
			out = append(out, Change{Kind: KindIndexAdded, Table: td.Name, Subject: ix.Name})
		}
		for _, ix := range td.RemovedIndexes {
			out = append(out, Change{Kind: KindIndexRemoved, Table: td.Name, Subject: ix.Name})
		}
	}

	return out
}

// specToChanges converts a Spec into a flat list of expected Changes.
func specToChanges(spec *Spec) []Change {
	var out []Change

	for _, t := range spec.Schema.AddedTables {
		out = append(out, Change{Kind: KindTableAdded, Table: t})
	}
	for _, t := range spec.Schema.RemovedTables {
		out = append(out, Change{Kind: KindTableRemoved, Table: t})
	}
	for _, tc := range spec.Schema.ModifiedTables {
		for _, c := range tc.AddedColumns {
			out = append(out, Change{Kind: KindColumnAdded, Table: tc.Table, Subject: c.Name})
		}
		for _, c := range tc.RemovedColumns {
			out = append(out, Change{Kind: KindColumnRemoved, Table: tc.Table, Subject: c.Name})
		}
		for _, ix := range tc.AddedIndexes {
			out = append(out, Change{Kind: KindIndexAdded, Table: tc.Table, Subject: ix.Name})
		}
		for _, ix := range tc.RemovedIndexes {
			out = append(out, Change{Kind: KindIndexRemoved, Table: tc.Table, Subject: ix.Name})
		}
	}

	return out
}
