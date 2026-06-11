package fleet

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestConfigSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fleet.yaml")

	cfg := &Config{
		Name: "prod",
		Databases: []Database{
			{Name: "users", DSN: "users.db", Tags: []string{"region:us"}},
			{Name: "orders", DSN: "orders.db"},
		},
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "prod" || len(loaded.Databases) != 2 {
		t.Fatalf("loaded = %+v", loaded)
	}
	if loaded.Version != configVersion {
		t.Errorf("version = %d, want %d", loaded.Version, configVersion)
	}
	if loaded.Databases[0].Tags[0] != "region:us" {
		t.Errorf("tags not preserved: %+v", loaded.Databases[0])
	}
}

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"empty", Config{}, true},
		{"no name", Config{Databases: []Database{{DSN: "x.db"}}}, true},
		{"no dsn", Config{Databases: []Database{{Name: "a"}}}, true},
		{"duplicate", Config{Databases: []Database{{Name: "a", DSN: "x"}, {Name: "a", DSN: "y"}}}, true},
		{"valid", Config{Databases: []Database{{Name: "a", DSN: "x"}}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("validate() err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestBaselinePath(t *testing.T) {
	cfg := &Config{}
	got := cfg.BaselinePath(Database{Name: "users"})
	want := filepath.Join(".litescope", "baselines", "users.json")
	if got != want {
		t.Errorf("BaselinePath = %q, want %q", got, want)
	}

	explicit := cfg.BaselinePath(Database{Name: "users", Baseline: "custom/path.json"})
	if explicit != "custom/path.json" {
		t.Errorf("explicit baseline = %q", explicit)
	}

	cfg.BaselinesDir = "snaps"
	if got := cfg.BaselinePath(Database{Name: "x"}); got != filepath.Join("snaps", "x.json") {
		t.Errorf("custom dir baseline = %q", got)
	}
}

func TestFilter(t *testing.T) {
	cfg := &Config{Databases: []Database{
		{Name: "a", DSN: "a", Tags: []string{"prod"}},
		{Name: "b", DSN: "b", Tags: []string{"staging"}},
		{Name: "c", DSN: "c", Tags: []string{"prod", "us"}},
	}}
	if got := cfg.Filter(""); len(got) != 3 {
		t.Errorf("Filter(\"\") = %d, want 3", len(got))
	}
	if got := cfg.Filter("prod"); len(got) != 2 {
		t.Errorf("Filter(prod) = %d, want 2", len(got))
	}
	if got := cfg.Filter("nope"); len(got) != 0 {
		t.Errorf("Filter(nope) = %d, want 0", len(got))
	}
}

func TestMerge(t *testing.T) {
	cfg := &Config{Databases: []Database{
		{Name: "a", DSN: "old-a", Baseline: "a.json"},
	}}
	added, updated := cfg.Merge([]Database{
		{Name: "a", DSN: "new-a"},
		{Name: "b", DSN: "b"},
	})
	if added != 1 || updated != 1 {
		t.Errorf("added=%d updated=%d, want 1,1", added, updated)
	}
	// Existing baseline preserved, DSN updated.
	var a *Database
	for i := range cfg.Databases {
		if cfg.Databases[i].Name == "a" {
			a = &cfg.Databases[i]
		}
	}
	if a == nil || a.DSN != "new-a" || a.Baseline != "a.json" {
		t.Errorf("merge clobbered entry: %+v", a)
	}
	// Sorted by name.
	if cfg.Databases[0].Name != "a" || cfg.Databases[1].Name != "b" {
		t.Errorf("not sorted: %+v", cfg.Databases)
	}
}

// makeDB writes a SQLite file with the given schema and returns its path.
func makeDB(t *testing.T, name, ddl string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckFleet(t *testing.T) {
	// Two local DBs; snapshot both, then mutate one and check.
	usersDB := makeDB(t, "users.db", `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);`)
	ordersDB := makeDB(t, "orders.db", `CREATE TABLE orders (id INTEGER PRIMARY KEY);`)

	baselineDir := t.TempDir()
	cfg := &Config{
		Name:         "test",
		BaselinesDir: baselineDir,
		Databases: []Database{
			{Name: "users", DSN: usersDB},
			{Name: "orders", DSN: ordersDB},
		},
	}

	// Baseline everything — both should report ok afterward.
	snaps := Snapshot(cfg, cfg.Databases, 4)
	for _, s := range snaps {
		if s.Err != nil {
			t.Fatalf("snapshot %s: %v", s.Database, s.Err)
		}
	}

	report := Check(cfg, cfg.Databases, 4)
	if ok, drift, _, errc := report.Counts(); ok != 2 || drift != 0 || errc != 0 {
		t.Fatalf("after snapshot: ok=%d drift=%d err=%d, want 2,0,0", ok, drift, errc)
	}

	// Mutate users → drift on exactly one database.
	db, _ := sql.Open("sqlite", usersDB)
	db.Exec(`ALTER TABLE users ADD COLUMN email TEXT;`)
	db.Close()

	report = Check(cfg, cfg.Databases, 4)
	ok, drift, _, errc := report.Counts()
	if ok != 1 || drift != 1 || errc != 0 {
		t.Fatalf("after mutation: ok=%d drift=%d err=%d, want 1,1,0", ok, drift, errc)
	}
	if !report.HasProblems() {
		t.Error("HasProblems should be true with drift present")
	}
}

func TestCheckFleet_NoBaseline(t *testing.T) {
	usersDB := makeDB(t, "users.db", `CREATE TABLE users (id INTEGER PRIMARY KEY);`)
	cfg := &Config{
		BaselinesDir: t.TempDir(), // empty — no baselines exist
		Databases:    []Database{{Name: "users", DSN: usersDB}},
	}
	report := Check(cfg, cfg.Databases, 1)
	if _, _, noBaseline, _ := report.Counts(); noBaseline != 1 {
		t.Errorf("expected 1 no-baseline result, got report %+v", report.Results)
	}
}

func TestCheckFleet_Error(t *testing.T) {
	// Baseline exists but the live source can't be read as a database → error.
	baselineDir := t.TempDir()
	realDB := makeDB(t, "real.db", `CREATE TABLE t (id INTEGER);`)
	cfg := &Config{
		BaselinesDir: baselineDir,
		Databases:    []Database{{Name: "ghost", DSN: realDB}},
	}
	if s := Snapshot(cfg, cfg.Databases, 1); s[0].Err != nil {
		t.Fatal(s[0].Err)
	}
	// Point the DSN at a directory — SQLite cannot open it as a database file.
	// (A nonexistent path would be silently created as an empty DB.)
	cfg.Databases[0].DSN = t.TempDir()

	report := Check(cfg, cfg.Databases, 1)
	if _, _, _, errc := report.Counts(); errc != 1 {
		t.Errorf("expected 1 error result, got %+v", report.Results)
	}
}
