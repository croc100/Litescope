package connector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/croc100/litescope/internal/schema"
)

type tursoConnector struct {
	dsn    string
	url    string
	token  string
	client *http.Client
}

func openTurso(dsn string) (Connector, error) {
	token, org, dbName, err := parseTursoDSN(dsn)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://%s-%s.turso.io", dbName, org)

	return &tursoConnector{
		dsn:   dsn,
		url:   url,
		token: token,
		client: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (t *tursoConnector) DSN() string  { return t.dsn }
func (t *tursoConnector) Close() error { return nil }

func (t *tursoConnector) Schema() (*schema.Schema, error) {
	// Load table names
	tableNames, err := t.queryColumn(
		"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name",
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("listing tables: %w", err)
	}

	var tables []schema.Table
	for _, name := range tableNames {
		cols, err := t.loadColumns(name)
		if err != nil {
			return nil, fmt.Errorf("columns for %s: %w", name, err)
		}
		idxs, err := t.loadIndexes(name)
		if err != nil {
			return nil, fmt.Errorf("indexes for %s: %w", name, err)
		}
		tables = append(tables, schema.Table{
			Name:    name,
			Columns: cols,
			Indexes: idxs,
		})
	}

	return &schema.Schema{Tables: tables}, nil
}

func (t *tursoConnector) loadColumns(table string) ([]schema.Column, error) {
	rows, err := t.execute(fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return nil, err
	}

	var cols []schema.Column
	for _, row := range rows {
		if len(row) < 6 {
			continue
		}
		cols = append(cols, schema.Column{
			Name:    asString(row[1]),
			Type:    asString(row[2]),
			NotNull: asString(row[3]) == "1",
			Default: asString(row[4]),
			PK:      asInt(row[5]),
		})
	}
	return cols, nil
}

func (t *tursoConnector) loadIndexes(table string) ([]schema.Index, error) {
	rows, err := t.execute(fmt.Sprintf("PRAGMA index_list(%q)", table))
	if err != nil {
		return nil, err
	}

	var idxs []schema.Index
	for _, row := range rows {
		if len(row) < 3 {
			continue
		}
		idxs = append(idxs, schema.Index{
			Name:   asString(row[1]),
			Unique: asString(row[2]) == "1",
		})
	}
	return idxs, nil
}

// ── Turso HTTP API ────────────────────────────────────────────────────────────

type pipelineRequest struct {
	Requests []pipelineStep `json:"requests"`
}

type pipelineStep struct {
	Type string   `json:"type"`
	Stmt *sqlStmt `json:"stmt,omitempty"`
}

type sqlStmt struct {
	SQL string `json:"sql"`
}

type pipelineResponse struct {
	Baton   string           `json:"baton"`
	Results []pipelineResult `json:"results"`
}

type pipelineResult struct {
	Type     string          `json:"type"`
	Response *executeResp    `json:"response,omitempty"`
	Error    *pipelineError  `json:"error,omitempty"`
}

type executeResp struct {
	Type   string        `json:"type"`
	Result *executeResult `json:"result"`
}

type executeResult struct {
	Cols []colDef        `json:"cols"`
	Rows [][]interface{} `json:"rows"`
}

type colDef struct {
	Name string `json:"name"`
}

type pipelineError struct {
	Message string `json:"message"`
}

func (t *tursoConnector) execute(sql string) ([][]interface{}, error) {
	body := pipelineRequest{
		Requests: []pipelineStep{
			{Type: "execute", Stmt: &sqlStmt{SQL: sql}},
			{Type: "close"},
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", t.url+"/v2/pipeline", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("turso request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("turso HTTP %d: %s", resp.StatusCode, string(b))
	}

	var pr pipelineResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("decoding turso response: %w", err)
	}

	if len(pr.Results) == 0 {
		return nil, nil
	}

	res := pr.Results[0]
	if res.Error != nil {
		return nil, fmt.Errorf("turso query error: %s", res.Error.Message)
	}
	if res.Response == nil || res.Response.Result == nil {
		return nil, nil
	}

	return res.Response.Result.Rows, nil
}

func (t *tursoConnector) Capabilities() ExecCapabilities {
	return ExecCapabilities{Transactional: true, LocalBackup: false, Provider: "turso"}
}

// Exec applies statements transactionally over the Hrana pipeline API.
//
// Phase 1 opens a connection (baton) and runs BEGIN + statements +
// foreign_key_check without committing. Phase 2 reuses the baton to COMMIT when
// everything succeeded, or ROLLBACK when any statement errored or a foreign key
// was violated. There is no local file backup for remote databases — safety
// comes from the transaction plus Turso's own point-in-time recovery.
func (t *tursoConnector) Exec(statements []string, dryRun bool) error {
	if len(statements) == 0 {
		return nil
	}

	steps := make([]pipelineStep, 0, len(statements)+2)
	steps = append(steps, pipelineStep{Type: "execute", Stmt: &sqlStmt{SQL: "BEGIN"}})
	for _, s := range statements {
		steps = append(steps, pipelineStep{Type: "execute", Stmt: &sqlStmt{SQL: s}})
	}
	steps = append(steps, pipelineStep{Type: "execute", Stmt: &sqlStmt{SQL: "PRAGMA foreign_key_check"}})

	resp, err := t.pipeline("", steps)
	if err != nil {
		return err
	}

	// Inspect every step; the last result is the foreign_key_check.
	applyErr := firstStepError(resp.Results)
	if applyErr == nil {
		applyErr = foreignKeyViolation(resp.Results[len(resp.Results)-1])
	}

	// Phase 2: finish the transaction on the same connection.
	finish := "COMMIT"
	if applyErr != nil || dryRun {
		finish = "ROLLBACK"
	}
	_, ferr := t.pipeline(resp.Baton, []pipelineStep{
		{Type: "execute", Stmt: &sqlStmt{SQL: finish}},
		{Type: "close"},
	})

	if applyErr != nil {
		return fmt.Errorf("turso migration rolled back: %w", applyErr)
	}
	if ferr != nil {
		return fmt.Errorf("turso commit failed: %w", ferr)
	}
	return nil
}

// pipeline sends a raw Hrana pipeline request, optionally continuing an existing
// connection via baton. It returns the decoded response (transport errors only;
// per-step SQL errors are carried in the results).
func (t *tursoConnector) pipeline(baton string, steps []pipelineStep) (*pipelineResponse, error) {
	body := struct {
		Baton    string         `json:"baton,omitempty"`
		Requests []pipelineStep `json:"requests"`
	}{Baton: baton, Requests: steps}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", t.url+"/v2/pipeline", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("turso request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("turso HTTP %d: %s", resp.StatusCode, string(b))
	}

	var pr pipelineResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("decoding turso response: %w", err)
	}
	return &pr, nil
}

// firstStepError returns the first per-step SQL error in a pipeline response.
func firstStepError(results []pipelineResult) error {
	for i, r := range results {
		if r.Error != nil {
			return fmt.Errorf("statement %d: %s", i, r.Error.Message)
		}
	}
	return nil
}

// foreignKeyViolation reports an error when a foreign_key_check result has rows.
func foreignKeyViolation(r pipelineResult) error {
	if r.Response == nil || r.Response.Result == nil {
		return nil
	}
	if n := len(r.Response.Result.Rows); n > 0 {
		return fmt.Errorf("%d foreign key violation(s) after migration", n)
	}
	return nil
}

func (t *tursoConnector) queryColumn(sql string, colIdx int) ([]string, error) {
	rows, err := t.execute(sql)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, row := range rows {
		if len(row) > colIdx {
			out = append(out, asString(row[colIdx]))
		}
	}
	return out, nil
}

// ── Type helpers ──────────────────────────────────────────────────────────────

func asString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return fmt.Sprintf("%g", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

func asInt(v interface{}) int {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int(val)
	case string:
		if val == "1" {
			return 1
		}
		return 0
	default:
		return 0
	}
}
