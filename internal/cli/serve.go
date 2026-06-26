package cli

import (
	"database/sql"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/croc100/litescope/internal/dashboard"
	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/fleet"
	"github.com/croc100/litescope/internal/importer"
	"github.com/croc100/litescope/internal/license"
	"github.com/croc100/litescope/internal/schema"
	"github.com/spf13/cobra"
	_ "modernc.org/sqlite"
)

func cmdServe() *cobra.Command {
	var configPath string
	var tag string
	var addr string
	var deep bool
	var noOpen bool
	var importDir string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Launch the local fleet dashboard in a browser (free)",
		Long: `Serve a local, self-hosted web dashboard for your fleet — topology map,
worst-first health, and schema fingerprint, all in the browser.

It runs entirely on this machine: no cloud, no account, no telemetry. Free,
including self-hosting on your own server. (The hosted, multi-user, org-scoped
dashboard with SSO and time-series history is a separate Enterprise offering.)

Examples:
  litescope serve
  litescope serve --config litescope.fleet.yaml --addr 127.0.0.1:7575
  litescope serve --tag region=eu --deep`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, dbs, err := loadFleet(configPath, tag)
			if err != nil {
				return err
			}

			fullTotal := len(dbs)
			// Free sees a read-only preview of the fleet; Pro sees all of it.
			// The dashboard is read-only, so the preview cap applies the same way
			// it does to `fleet fingerprint` / `fleet health`.
			shown := freePreviewFleet(dbs)
			preview := !license.IsPro() && fullTotal > len(shown)

			// Databases added at runtime by dropping a file onto the dashboard.
			var mu sync.Mutex
			var imported []fleet.Database

			currentDBs := func() []fleet.Database {
				mu.Lock()
				defer mu.Unlock()
				all := make([]fleet.Database, 0, len(shown)+len(imported))
				all = append(all, shown...)
				all = append(all, imported...)
				return all
			}

			provider := func() (*dashboard.Overview, error) {
				dbList := currentDBs()
				health := fleet.Health(dbList, deep, 0)
				fp := fleet.Fingerprint(dbList, 0)
				name := cfg.Name
				if name == "" {
					name = "fleet"
				}
				return &dashboard.Overview{
					FleetName:   name,
					Total:       len(dbList),
					Preview:     preview,
					PreviewCap:  len(shown),
					FullTotal:   fullTotal + len(imported),
					Health:      health,
					Fingerprint: fp,
					GeneratedAt: time.Now().UTC(),
				}, nil
			}

			srv := dashboard.New(provider)
			srv.SetImportHandler(func(filename string, data io.Reader) (string, error) {
				name, dsn, table, err := importDropped(importDir, filename, data)
				if err != nil {
					return "", err
				}
				mu.Lock()
				defer mu.Unlock()
				// Replace any existing entry with the same name, else append.
				replaced := false
				for i := range imported {
					if imported[i].Name == name {
						imported[i].DSN = dsn
						replaced = true
						break
					}
				}
				if !replaced {
					imported = append(imported, fleet.Database{Name: name, DSN: dsn, Tags: []string{"imported"}})
				}
				return fmt.Sprintf("%s → table %s", filepath.Base(dsn), table), nil
			})
			// Read-only data browser + SQL console over the fleet's local databases.
			resolveDSN := func(name string) (string, error) {
				for _, db := range currentDBs() {
					if db.Name == name {
						return db.DSN, nil
					}
				}
				return "", fmt.Errorf("unknown database %q", name)
			}
			srv.SetDataBrowser(
				func(name string) ([]dashboard.TableInfo, error) {
					dsn, err := resolveDSN(name)
					if err != nil {
						return nil, err
					}
					return listTables(dsn)
				},
				func(name, query string) (*dashboard.QueryResult, error) {
					dsn, err := resolveDSN(name)
					if err != nil {
						return nil, err
					}
					return runReadOnlyQuery(dsn, query)
				},
			)
			srv.SetTableBrowser(func(name, table, orderBy, dir string, limit, offset int) (*dashboard.BrowseResult, error) {
				dsn, err := resolveDSN(name)
				if err != nil {
					return nil, err
				}
				return browseTable(dsn, table, orderBy, dir, limit, offset)
			})
			// Interactive ERD over the fleet's local databases.
			srv.SetSchemaProvider(func(name string) (*dashboard.SchemaGraph, error) {
				dsn, err := resolveDSN(name)
				if err != nil {
					return nil, err
				}
				g, err := schemaGraph(dsn)
				if err != nil {
					return nil, err
				}
				// Overlay the fleet fingerprint: show how this one database's
				// schema deviates from the fleet's canonical schema. This is what
				// turns a single-DB ERD into a fleet-operations view.
				annotateSchemaFingerprint(g, name, fleet.Fingerprint(currentDBs(), 0))
				return g, nil
			})
			// Visual schema/data diff between any two databases in the fleet.
			srv.SetDiffProvider(func(oldName, newName string) (*dashboard.DiffResult, error) {
				oldDSN, err := resolveDSN(oldName)
				if err != nil {
					return nil, err
				}
				newDSN, err := resolveDSN(newName)
				if err != nil {
					return nil, err
				}
				return diffDatabases(oldDSN, newDSN)
			})

			url := "http://" + addr

			fmt.Printf("\n  %s  Litescope dashboard\n", styleOK.Render("◎"))
			fmt.Printf("  %s  Fleet:  %s (%d database(s))\n", styleDim.Render("·"), cfg.Name, fullTotal)
			if preview {
				fmt.Printf("  %s  %s\n", styleWarn.Render("!"),
					styleWarn.Render(fmt.Sprintf("Free preview — showing %d of %d. Full fleet with Pro.", len(shown), fullTotal)))
			}
			fmt.Printf("  %s  URL:    %s\n", styleDim.Render("·"), url)
			fmt.Printf("\n  Press Ctrl+C to stop.\n\n")

			if !noOpen {
				openBrowser(url)
			}

			return srv.ListenAndServe(addr)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "", "fleet config file (default litescope.fleet.yaml)")
	cmd.Flags().StringVar(&tag, "tag", "", "only include databases matching tag (key=value or key)")
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:7575", "address to listen on")
	cmd.Flags().BoolVar(&deep, "deep", false, "run exhaustive integrity_check on each database")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "do not open a browser automatically")
	cmd.Flags().StringVar(&importDir, "import-dir", ".", "directory for databases created by drag-drop import")
	return cmd
}

// importDropped ingests a file dropped on the dashboard into its own SQLite
// database under dir, returning the fleet display name, the database path, and
// the created table name.
func importDropped(dir, filename string, data io.Reader) (name, dsn, table string, err error) {
	format := detectFormat(filename)
	name = sanitizeTableName(filename)
	dsn = filepath.Join(dir, name+".db")

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", "", "", err
	}
	defer db.Close()

	// A fresh drop replaces any prior import of the same name.
	opt := importer.Options{Table: name, Mode: importer.ModeReplace, HasHeader: true}
	var res *importer.Result
	switch format {
	case "tsv":
		opt.Delimiter = '\t'
		res, err = importer.ImportCSV(db, data, opt)
	case "json":
		res, err = importer.ImportJSON(db, data, opt)
	case "xlsx":
		res, err = importer.ImportXLSX(db, data, opt)
	default:
		res, err = importer.ImportCSV(db, data, opt)
	}
	if err != nil {
		return "", "", "", err
	}
	return name, dsn, res.Table, nil
}

// serveQueryMaxRows caps how many rows a dashboard query returns.
const serveQueryMaxRows = 2000

// localDSNPath returns the file path of a local SQLite DSN. Remote DSNs
// (turso://, d1://) are not browsable from the free local dashboard.
func localDSNPath(dsn string) (string, error) {
	switch {
	case strings.HasPrefix(dsn, "turso://"), strings.HasPrefix(dsn, "d1://"):
		return "", fmt.Errorf("data browser supports local SQLite only (this database is remote)")
	default:
		return dsn, nil
	}
}

// openReadOnly opens a local SQLite database read-only and enforces query_only
// at the engine level — writes are rejected regardless of the SQL submitted.
func openReadOnly(dsn string) (*sql.DB, error) {
	path, err := localDSNPath(dsn)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA query_only=ON"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// schemaGraph loads the ERD graph (tables, columns, foreign keys) of a local
// database. Remote DSNs are rejected — the ERD is a local-only feature.
func schemaGraph(dsn string) (*dashboard.SchemaGraph, error) {
	path, err := localDSNPath(dsn)
	if err != nil {
		return nil, fmt.Errorf("ERD supports local SQLite only (this database is remote)")
	}
	s, err := schema.Load(path)
	if err != nil {
		return nil, err
	}
	g := &dashboard.SchemaGraph{}
	for _, t := range s.Tables {
		fkCols := make(map[string]bool, len(t.ForeignKeys))
		for _, fk := range t.ForeignKeys {
			fkCols[fk.From] = true
			g.Edges = append(g.Edges, dashboard.SchemaEdge{From: t.Name, To: fk.Table, Column: fk.From})
		}
		st := dashboard.SchemaTable{Name: t.Name}
		for _, c := range t.Columns {
			st.Columns = append(st.Columns, dashboard.SchemaColumn{
				Name: c.Name, Type: c.Type, PK: c.PK > 0, FK: fkCols[c.Name],
			})
		}
		g.Tables = append(g.Tables, st)
	}
	return g, nil
}

// diffDatabases compares two local databases (old → new) and curates the result
// for the dashboard's diff panel. Remote sources are rejected (the data diff
// needs direct file access). oldDSN/newDSN are resolved fleet DSNs.
func diffDatabases(oldDSN, newDSN string) (*dashboard.DiffResult, error) {
	oldPath, err := localDSNPath(oldDSN)
	if err != nil {
		return nil, fmt.Errorf("diff supports local SQLite only (old database is remote)")
	}
	newPath, err := localDSNPath(newDSN)
	if err != nil {
		return nil, fmt.Errorf("diff supports local SQLite only (new database is remote)")
	}

	res, err := diff.Compare(oldPath, newPath)
	if err != nil {
		return nil, err
	}

	out := &dashboard.DiffResult{Old: oldPath, New: newPath}
	for _, t := range res.Schema {
		dt := dashboard.DiffTable{Name: t.Name}
		switch {
		case t.Added:
			dt.Status = "added"
		case t.Removed:
			dt.Status = "removed"
		default:
			dt.Status = "changed"
		}
		for _, c := range t.AddedColumns {
			dt.AddedColumns = append(dt.AddedColumns, c.Name)
		}
		for _, c := range t.RemovedColumns {
			dt.RemovedColumns = append(dt.RemovedColumns, c.Name)
		}
		for _, c := range t.ChangedColumns {
			cc := dashboard.DiffColumnChange{Name: c.Name}
			if c.Old != nil {
				cc.OldType = c.Old.Type
			}
			if c.New != nil {
				cc.NewType = c.New.Type
			}
			dt.ChangedColumns = append(dt.ChangedColumns, cc)
		}
		for _, ix := range t.AddedIndexes {
			dt.AddedIndexes = append(dt.AddedIndexes, ix.Name)
		}
		for _, ix := range t.RemovedIndexes {
			dt.RemovedIndexes = append(dt.RemovedIndexes, ix.Name)
		}
		out.Schema = append(out.Schema, dt)
	}
	for _, d := range res.Data {
		if d.Added == 0 && d.Removed == 0 && d.Changed == 0 {
			continue
		}
		out.Data = append(out.Data, dashboard.DiffData{
			Table: d.Table, Added: d.Added, Removed: d.Removed, Changed: d.Changed,
		})
	}
	out.Identical = len(out.Schema) == 0 && len(out.Data) == 0
	return out, nil
}

// annotateSchemaFingerprint overlays the fleet fingerprint onto a single
// database's ERD graph: it records which schema cluster the database belongs to
// and, when that cluster has drifted from canonical, marks each table and column
// that differs. Ghost entries are added for tables/columns that canonical has
// but this database is missing. It is a no-op for fleets with no clusters or a
// database that could not be fingerprinted.
func annotateSchemaFingerprint(g *dashboard.SchemaGraph, dbName string, fp *fleet.FingerprintReport) {
	if g == nil || fp == nil || len(fp.Clusters) == 0 {
		return
	}
	var canonID string
	var cluster *fleet.FingerprintCluster
	for i := range fp.Clusters {
		if fp.Clusters[i].IsCanonical {
			canonID = fp.Clusters[i].ID
		}
		for _, m := range fp.Clusters[i].Members {
			if m == dbName {
				cluster = &fp.Clusters[i]
			}
		}
	}
	if cluster == nil {
		return
	}

	info := &dashboard.SchemaFingerprint{
		ClusterID:    cluster.ID,
		IsCanonical:  cluster.IsCanonical,
		CanonicalID:  canonID,
		ClusterCount: cluster.Count,
		FleetTotal:   fp.Total,
	}
	g.Fingerprint = info
	if cluster.IsCanonical {
		return
	}

	// cluster.Drift is the diff from canonical → this cluster's schema, so
	// "Added" means present here, "Removed" means present in canonical only.
	idx := make(map[string]int, len(g.Tables))
	for i := range g.Tables {
		idx[g.Tables[i].Name] = i
	}

	for _, td := range cluster.Drift {
		switch {
		case td.Added:
			if i, ok := idx[td.Name]; ok {
				g.Tables[i].Drift = "added"
			}
			info.DriftTables++
		case td.Removed:
			ghost := dashboard.SchemaTable{Name: td.Name, Ghost: true}
			for _, c := range td.RemovedColumns {
				ghost.Columns = append(ghost.Columns, dashboard.SchemaColumn{
					Name: c.Name, Type: c.Type, PK: c.PK > 0, Drift: "missing",
				})
			}
			g.Tables = append(g.Tables, ghost)
			info.DriftTables++
		default:
			i, ok := idx[td.Name]
			if !ok {
				continue
			}
			added := make(map[string]bool, len(td.AddedColumns))
			for _, c := range td.AddedColumns {
				added[c.Name] = true
			}
			changed := make(map[string]bool, len(td.ChangedColumns))
			for _, cc := range td.ChangedColumns {
				changed[cc.Name] = true
			}
			for ci := range g.Tables[i].Columns {
				switch cn := g.Tables[i].Columns[ci].Name; {
				case added[cn]:
					g.Tables[i].Columns[ci].Drift = "added"
					info.DriftColumns++
				case changed[cn]:
					g.Tables[i].Columns[ci].Drift = "changed"
					info.DriftColumns++
				}
			}
			for _, c := range td.RemovedColumns {
				g.Tables[i].Columns = append(g.Tables[i].Columns, dashboard.SchemaColumn{
					Name: c.Name, Type: c.Type, PK: c.PK > 0, Drift: "missing",
				})
				info.DriftColumns++
			}
			info.DriftTables++
		}
	}
}

// listTables returns the user tables of a local database with row counts,
// ordered by name.
func listTables(dsn string) ([]dashboard.TableInfo, error) {
	db, err := openReadOnly(dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT name FROM sqlite_master
		WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, n)
	}
	rows.Close()

	out := make([]dashboard.TableInfo, 0, len(names))
	for _, n := range names {
		var count int64
		// Table names come from sqlite_master, not user input, but quote to be safe.
		_ = db.QueryRow(`SELECT count(*) FROM "` + strings.ReplaceAll(n, `"`, `""`) + `"`).Scan(&count)
		out = append(out, dashboard.TableInfo{Name: n, Rows: count})
	}
	return out, nil
}

// serveBrowseMaxLimit caps the page size a single browse request may return.
const serveBrowseMaxLimit = 1000

// quoteIdent wraps a SQLite identifier in double quotes, escaping embedded ones.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// browseTable returns one paginated, optionally sorted page of a table. The
// table and sort column are validated against the live schema so neither can be
// used for SQL injection; pagination is enforced server-side.
func browseTable(dsn, table, orderBy, dir string, limit, offset int) (*dashboard.BrowseResult, error) {
	if strings.TrimSpace(table) == "" {
		return nil, fmt.Errorf("no table specified")
	}
	db, err := openReadOnly(dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// Resolve and validate the table's columns; this also rejects unknown tables.
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	valid := map[string]bool{}
	var cols []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		valid[n] = true
		cols = append(cols, n)
	}
	rows.Close()
	if len(cols) == 0 {
		return nil, fmt.Errorf("unknown table %q", table)
	}

	// Sort column must be a real column; direction is normalized to ASC/DESC.
	if orderBy != "" && !valid[orderBy] {
		return nil, fmt.Errorf("unknown sort column %q", orderBy)
	}
	dir = strings.ToLower(strings.TrimSpace(dir))
	if dir != "desc" {
		dir = "asc"
	}

	if limit <= 0 {
		limit = 100
	}
	if limit > serveBrowseMaxLimit {
		limit = serveBrowseMaxLimit
	}
	if offset < 0 {
		offset = 0
	}

	q := "SELECT * FROM " + quoteIdent(table)
	if orderBy != "" {
		q += " ORDER BY " + quoteIdent(orderBy) + " " + strings.ToUpper(dir)
	}
	q += " LIMIT ? OFFSET ?"

	res := &dashboard.BrowseResult{Offset: offset, Limit: limit, OrderBy: orderBy}
	if orderBy != "" {
		res.Dir = dir
	}
	_ = db.QueryRow("SELECT count(*) FROM "+quoteIdent(table)).Scan(&res.Total)

	dataRows, err := db.Query(q, limit, offset)
	if err != nil {
		return nil, err
	}
	defer dataRows.Close()
	res.Columns, _ = dataRows.Columns()
	res.Rows = [][]any{}
	for dataRows.Next() {
		vals := make([]any, len(res.Columns))
		ptrs := make([]any, len(res.Columns))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := dataRows.Scan(ptrs...); err != nil {
			continue
		}
		row := make([]any, len(res.Columns))
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				row[i] = string(b)
			} else {
				row[i] = v
			}
		}
		res.Rows = append(res.Rows, row)
	}
	return res, dataRows.Err()
}

// runReadOnlyQuery executes a SQL query against a local database opened
// read-only. Mutations fail at the engine level (query_only=ON).
func runReadOnlyQuery(dsn, query string) (*dashboard.QueryResult, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, fmt.Errorf("empty query")
	}
	db, err := openReadOnly(dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	start := time.Now()
	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, _ := rows.Columns()
	res := &dashboard.QueryResult{Columns: cols, Rows: [][]any{}}
	for rows.Next() {
		if len(res.Rows) >= serveQueryMaxRows {
			res.Truncated = true
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			continue
		}
		row := make([]any, len(cols))
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				row[i] = string(b)
			} else {
				row[i] = v
			}
		}
		res.Rows = append(res.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	res.DurationMs = time.Since(start).Milliseconds()
	return res, nil
}

// openBrowser tries to open url in the default browser; failure is non-fatal.
func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}
	args = append(args, url)
	_ = exec.Command(cmd, args...).Start()
}
