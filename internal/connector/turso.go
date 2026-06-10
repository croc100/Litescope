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
