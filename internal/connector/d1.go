package connector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/croc100/litescope/internal/schema"
)

// d1Connector connects to a Cloudflare D1 database via the Workers API.
//
// DSN: d1://TOKEN@ACCOUNT_ID/DATABASE_ID
type d1Connector struct {
	dsn        string
	accountID  string
	databaseID string
	token      string
	client     *http.Client
}

// openD1 parses d1://TOKEN@ACCOUNT_ID/DATABASE_ID and returns a connector.
func openD1(dsn string) (Connector, error) {
	token, accountID, databaseID, err := parseD1DSN(dsn)
	if err != nil {
		return nil, err
	}
	return &d1Connector{
		dsn:        dsn,
		accountID:  accountID,
		databaseID: databaseID,
		token:      token,
		client:     &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func parseD1DSN(dsn string) (token, accountID, databaseID string, err error) {
	rest := strings.TrimPrefix(dsn, "d1://")

	atIdx := strings.Index(rest, "@")
	if atIdx < 0 {
		return "", "", "", fmt.Errorf("d1 DSN must be d1://TOKEN@ACCOUNT_ID/DATABASE_ID, got: %s", dsn)
	}
	token = rest[:atIdx]
	rest = rest[atIdx+1:]

	slashIdx := strings.Index(rest, "/")
	if slashIdx < 0 {
		return "", "", "", fmt.Errorf("d1 DSN must be d1://TOKEN@ACCOUNT_ID/DATABASE_ID, got: %s", dsn)
	}
	accountID = rest[:slashIdx]
	databaseID = rest[slashIdx+1:]

	if token == "" || accountID == "" || databaseID == "" {
		return "", "", "", fmt.Errorf("d1 DSN missing token, account_id, or database_id: %s", dsn)
	}
	return token, accountID, databaseID, nil
}

func (d *d1Connector) DSN() string  { return d.dsn }
func (d *d1Connector) Close() error { return nil }

func (d *d1Connector) Schema() (*schema.Schema, error) {
	tableNames, err := d.queryScalar(
		"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '_cf_%' ORDER BY name",
		"name",
	)
	if err != nil {
		return nil, fmt.Errorf("listing tables: %w", err)
	}

	var tables []schema.Table
	for _, name := range tableNames {
		cols, err := d.loadColumns(name)
		if err != nil {
			return nil, fmt.Errorf("columns for %s: %w", name, err)
		}
		idxs, err := d.loadIndexes(name)
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

func (d *d1Connector) loadColumns(table string) ([]schema.Column, error) {
	rows, err := d.execute(fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return nil, err
	}

	var cols []schema.Column
	for _, row := range rows {
		cols = append(cols, schema.Column{
			Name:    rowStr(row, "name"),
			Type:    rowStr(row, "type"),
			NotNull: rowStr(row, "notnull") == "1",
			Default: rowStr(row, "dflt_value"),
			PK:      rowInt(row, "pk"),
		})
	}
	return cols, nil
}

func (d *d1Connector) loadIndexes(table string) ([]schema.Index, error) {
	rows, err := d.execute(fmt.Sprintf("PRAGMA index_list(%q)", table))
	if err != nil {
		return nil, err
	}

	var idxs []schema.Index
	for _, row := range rows {
		idxs = append(idxs, schema.Index{
			Name:   rowStr(row, "name"),
			Unique: rowStr(row, "unique") == "1",
		})
	}
	return idxs, nil
}

// ── Cloudflare D1 HTTP API ────────────────────────────────────────────────────

type d1Request struct {
	SQL    string        `json:"sql"`
	Params []interface{} `json:"params"`
}

type d1Response struct {
	Result  []d1QueryResult `json:"result"`
	Success bool            `json:"success"`
	Errors  []d1Error       `json:"errors"`
}

type d1QueryResult struct {
	Results []map[string]interface{} `json:"results"`
	Success bool                     `json:"success"`
	Meta    map[string]interface{}   `json:"meta"`
}

type d1Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (d *d1Connector) apiURL() string {
	return fmt.Sprintf(
		"https://api.cloudflare.com/client/v4/accounts/%s/d1/database/%s/query",
		d.accountID, d.databaseID,
	)
}

func (d *d1Connector) execute(sql string) ([]map[string]interface{}, error) {
	body, err := json.Marshal(d1Request{SQL: sql, Params: []interface{}{}})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", d.apiURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+d.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("D1 request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading D1 response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("D1 HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var d1Resp d1Response
	if err := json.Unmarshal(raw, &d1Resp); err != nil {
		return nil, fmt.Errorf("decoding D1 response: %w", err)
	}

	if !d1Resp.Success {
		msgs := make([]string, len(d1Resp.Errors))
		for i, e := range d1Resp.Errors {
			msgs[i] = fmt.Sprintf("[%d] %s", e.Code, e.Message)
		}
		return nil, fmt.Errorf("D1 error: %s", strings.Join(msgs, "; "))
	}

	if len(d1Resp.Result) == 0 || !d1Resp.Result[0].Success {
		return nil, fmt.Errorf("D1 query returned no result")
	}

	return d1Resp.Result[0].Results, nil
}

func (d *d1Connector) queryScalar(sql, col string) ([]string, error) {
	rows, err := d.execute(sql)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, row := range rows {
		if v, ok := row[col]; ok {
			out = append(out, fmt.Sprintf("%v", v))
		}
	}
	return out, nil
}

// ── Row helpers ───────────────────────────────────────────────────────────────

func rowStr(row map[string]interface{}, key string) string {
	v, ok := row[key]
	if !ok || v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func rowInt(row map[string]interface{}, key string) int {
	v, ok := row[key]
	if !ok || v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int(val)
	case bool:
		if val {
			return 1
		}
		return 0
	case string:
		if val == "1" || val == "true" {
			return 1
		}
		return 0
	}
	return 0
}
