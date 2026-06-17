package fleet

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/croc100/litescope/internal/connector"
	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/schema"
)

// FingerprintCluster is a group of databases that share an identical schema.
type FingerprintCluster struct {
	ID          string         `json:"id"`           // short hash of the canonical schema serialization
	Count       int            `json:"count"`        // number of databases in this cluster
	Members     []string       `json:"members"`      // database names, sorted
	IsCanonical bool           `json:"is_canonical"` // true for the reference cluster (largest by default)
	Drift       []diff.TableDiff `json:"drift,omitempty"` // how this cluster differs from canonical; nil for canonical
	schema      *schema.Schema // representative schema, not serialized
}

// FingerprintError records a database that could not be read.
type FingerprintError struct {
	Database string `json:"database"`
	Error    string `json:"error"`
}

// FingerprintReport is the result of fingerprinting an entire fleet.
type FingerprintReport struct {
	Total       int                 `json:"total"`       // databases successfully fingerprinted
	Clusters    []FingerprintCluster `json:"clusters"`   // canonical first, then by count desc
	Unreachable []FingerprintError  `json:"unreachable,omitempty"`
	CheckedAt   time.Time           `json:"checked_at"`
}

// fingerprintOne reads one database's live schema.
type fingerprintOne struct {
	db     Database
	schema *schema.Schema
	err    error
}

// Fingerprint reads every database's live schema in parallel and groups them
// into clusters of identical schemas. The largest cluster is marked canonical;
// every other cluster carries a diff describing how it differs from canonical.
//
// The fingerprint is computed over exactly the fields the diff engine treats as
// drift-significant (table presence, column name/type/not-null, index presence),
// so two databases share a fingerprint if and only if `fleet check` would report
// no drift between them.
func Fingerprint(dbs []Database, concurrency int) *FingerprintReport {
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}

	reads := make([]fingerprintOne, len(dbs))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, db := range dbs {
		wg.Add(1)
		go func(i int, db Database) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			reads[i] = readSchema(db)
		}(i, db)
	}
	wg.Wait()

	// Group successful reads by fingerprint hash.
	type group struct {
		schema  *schema.Schema
		members []string
	}
	groups := map[string]*group{}
	var unreachable []FingerprintError

	for _, r := range reads {
		if r.err != nil {
			unreachable = append(unreachable, FingerprintError{Database: r.db.Name, Error: r.err.Error()})
			continue
		}
		h := Hash(r.schema)
		g := groups[h]
		if g == nil {
			g = &group{schema: r.schema}
			groups[h] = g
		}
		g.members = append(g.members, r.db.Name)
	}

	clusters := make([]FingerprintCluster, 0, len(groups))
	for h, g := range groups {
		sort.Strings(g.members)
		clusters = append(clusters, FingerprintCluster{
			ID:      h[:8],
			Count:   len(g.members),
			Members: g.members,
			schema:  g.schema,
		})
	}

	// Sort by count desc, then by ID for stability. Largest = canonical.
	sort.Slice(clusters, func(i, j int) bool {
		if clusters[i].Count != clusters[j].Count {
			return clusters[i].Count > clusters[j].Count
		}
		return clusters[i].ID < clusters[j].ID
	})

	total := 0
	if len(clusters) > 0 {
		clusters[0].IsCanonical = true
		canonical := clusters[0].schema
		for i := range clusters {
			total += clusters[i].Count
			if !clusters[i].IsCanonical {
				d := diff.CompareSchemas(canonical, clusters[i].schema)
				clusters[i].Drift = d.Schema
			}
		}
	}

	return &FingerprintReport{
		Total:       total,
		Clusters:    clusters,
		Unreachable: unreachable,
		CheckedAt:   time.Now().UTC(),
	}
}

func readSchema(db Database) fingerprintOne {
	conn, err := connector.Open(db.DSN)
	if err != nil {
		return fingerprintOne{db: db, err: err}
	}
	defer conn.Close()
	s, err := conn.Schema()
	if err != nil {
		return fingerprintOne{db: db, err: err}
	}
	return fingerprintOne{db: db, schema: s}
}

// Hash returns a hex SHA-256 over a deterministic, drift-significant
// serialization of the schema. Order-independent: tables, columns, and indexes
// are all sorted so column/table declaration order does not affect the result.
func Hash(s *schema.Schema) string {
	sum := sha256.Sum256([]byte(canonicalString(s)))
	return hex.EncodeToString(sum[:])
}

func canonicalString(s *schema.Schema) string {
	if s == nil {
		return ""
	}
	tables := make([]schema.Table, len(s.Tables))
	copy(tables, s.Tables)
	sort.Slice(tables, func(i, j int) bool { return tables[i].Name < tables[j].Name })

	var b strings.Builder
	for _, t := range tables {
		fmt.Fprintf(&b, "T:%s\n", t.Name)

		cols := make([]schema.Column, len(t.Columns))
		copy(cols, t.Columns)
		sort.Slice(cols, func(i, j int) bool { return cols[i].Name < cols[j].Name })
		for _, c := range cols {
			// Only the fields the diff engine compares: name, type, not-null.
			fmt.Fprintf(&b, "C:%s|%s|%t\n", c.Name, c.Type, c.NotNull)
		}

		idxNames := make([]string, 0, len(t.Indexes))
		for _, ix := range t.Indexes {
			idxNames = append(idxNames, ix.Name)
		}
		sort.Strings(idxNames)
		for _, n := range idxNames {
			fmt.Fprintf(&b, "I:%s\n", n)
		}
	}
	return b.String()
}
