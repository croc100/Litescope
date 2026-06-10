package validate

import "github.com/croc100/litescope/internal/diff"

// SpecFromDiff generates a Spec from an actual diff result.
// Used by `litescope validate init` to bootstrap the expectation file.
func SpecFromDiff(d *diff.Result, description string) *Spec {
	spec := &Spec{
		Version:     specVersion,
		Description: description,
	}

	for _, td := range d.Schema {
		if td.Added {
			spec.Schema.AddedTables = append(spec.Schema.AddedTables, td.Name)
			continue
		}
		if td.Removed {
			spec.Schema.RemovedTables = append(spec.Schema.RemovedTables, td.Name)
			continue
		}

		tc := TableChange{Table: td.Name}
		for _, c := range td.AddedColumns {
			tc.AddedColumns = append(tc.AddedColumns, ColumnSpec{Name: c.Name, Type: c.Type})
		}
		for _, c := range td.RemovedColumns {
			tc.RemovedColumns = append(tc.RemovedColumns, ColumnSpec{Name: c.Name})
		}
		for _, ix := range td.AddedIndexes {
			tc.AddedIndexes = append(tc.AddedIndexes, IndexSpec{Name: ix.Name, Unique: ix.Unique})
		}
		for _, ix := range td.RemovedIndexes {
			tc.RemovedIndexes = append(tc.RemovedIndexes, IndexSpec{Name: ix.Name})
		}

		if len(tc.AddedColumns)+len(tc.RemovedColumns)+
			len(tc.AddedIndexes)+len(tc.RemovedIndexes) > 0 {
			spec.Schema.ModifiedTables = append(spec.Schema.ModifiedTables, tc)
		}
	}

	return spec
}
