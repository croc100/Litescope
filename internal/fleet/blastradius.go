package fleet

import (
	"sort"

	"github.com/croc100/litescope/internal/health"
)

// BlastGroup is a shared cohort (a tag, e.g. a Turso group) that contains at
// least one faulted database — so the rest of the cohort may be at risk from a
// shared-infrastructure problem.
type BlastGroup struct {
	Tag       string   `json:"tag"`        // shared tag, e.g. "group:prod"
	Total     int      `json:"total"`      // databases carrying this tag
	Faulted   []string `json:"faulted"`    // critically faulted members
	AtRisk    []string `json:"at_risk"`    // healthy members of the same cohort
}

// BlastRadius groups critically faulted databases by the tags they share, so a
// single fault report shows the blast radius across shared infrastructure
// (Turso groups, regions). Only cohorts containing a fault are returned, most
// faults first.
func BlastRadius(dbs []Database, report *HealthReport) []BlastGroup {
	faulted := map[string]bool{}
	for _, r := range report.Results {
		if r.Report.Severity == health.SevCritical {
			faulted[r.Database] = true
		}
	}

	// tag -> members (all), and tag -> faulted members
	members := map[string][]string{}
	for _, db := range dbs {
		for _, tag := range db.Tags {
			members[tag] = append(members[tag], db.Name)
		}
	}

	var groups []BlastGroup
	for tag, names := range members {
		var faultedNames, atRisk []string
		for _, n := range names {
			if faulted[n] {
				faultedNames = append(faultedNames, n)
			} else {
				atRisk = append(atRisk, n)
			}
		}
		if len(faultedNames) == 0 {
			continue // no fault in this cohort
		}
		sort.Strings(faultedNames)
		sort.Strings(atRisk)
		groups = append(groups, BlastGroup{
			Tag:     tag,
			Total:   len(names),
			Faulted: faultedNames,
			AtRisk:  atRisk,
		})
	}

	sort.Slice(groups, func(i, j int) bool {
		if len(groups[i].Faulted) != len(groups[j].Faulted) {
			return len(groups[i].Faulted) > len(groups[j].Faulted)
		}
		return groups[i].Tag < groups[j].Tag
	})
	return groups
}
