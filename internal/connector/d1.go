package connector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/croc100/litescope/internal/schema"
)

// d1Connector connects to a Cloudflare D1 database via the Workers API.
//
// DSN forms:
//
//	d1://TOKEN@ACCOUNT_ID/DATABASE_ID   — explicit credentials
//	d1://ACCOUNT_ID/DATABASE_ID         — token from CLOUDFLARE_API_TOKEN env var
//	d1://DATABASE_ID                    — token+account from env vars
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

// ParseD1DSN is the exported form of parseD1DSN for use by CLI commands.
func ParseD1DSN(dsn string) (token, accountID, databaseID string, err error) {
	return parseD1DSN(dsn)
}

func parseD1DSN(dsn string) (token, accountID, databaseID string, err error) {
	rest := strings.TrimPrefix(dsn, "d1://")

	atIdx := strings.Index(rest, "@")
	if atIdx >= 0 {
		// Full form: d1://TOKEN@ACCOUNT_ID/DATABASE_ID
		token = rest[:atIdx]
		rest = rest[atIdx+1:]
	} else {
		// Short form: resolve token from env
		token = os.Getenv("CLOUDFLARE_API_TOKEN")
		if token == "" {
			return "", "", "", fmt.Errorf(
				"d1 DSN has no token and CLOUDFLARE_API_TOKEN is not set; "+
					"use d1://TOKEN@ACCOUNT_ID/DB_ID or set CLOUDFLARE_API_TOKEN",
			)
		}
	}

	slashIdx := strings.Index(rest, "/")
	if slashIdx >= 0 {
		// ACCOUNT_ID/DATABASE_ID
		accountID = rest[:slashIdx]
		databaseID = rest[slashIdx+1:]
	} else {
		// Only DATABASE_ID — resolve account from env
		databaseID = rest
		accountID = os.Getenv("CLOUDFLARE_ACCOUNT_ID")
		if accountID == "" {
			return "", "", "", fmt.Errorf(
				"d1 DSN has no account_id and CLOUDFLARE_ACCOUNT_ID is not set; "+
					"use d1://ACCOUNT_ID/DB_ID or set CLOUDFLARE_ACCOUNT_ID",
			)
		}
	}

	if token == "" || accountID == "" || databaseID == "" {
		return "", "", "", fmt.Errorf("d1 DSN missing token, account_id, or database_id: %s", dsn)
	}
	return token, accountID, databaseID, nil
}

// D1TimeTravelResult holds the outcome of a Time Travel restore.
type D1TimeTravelResult struct {
	Bookmark  string `json:"bookmark"`
	Timestamp string `json:"timestamp"`
}

// D1TimeTravel restores a D1 database to the given point in time using
// Cloudflare's Time Travel API. Credentials are resolved from the environment
// (CLOUDFLARE_API_TOKEN) if not embedded in the DSN.
func D1TimeTravel(accountID, databaseID string, ts interface{ Unix() int64 }) (*D1TimeTravelResult, error) {
	token := os.Getenv("CLOUDFLARE_API_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("CLOUDFLARE_API_TOKEN must be set for Time Travel")
	}

	type restoreReq struct {
		Timestamp int64 `json:"timestamp"`
	}
	body, err := json.Marshal(restoreReq{Timestamp: ts.Unix()})
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 60 * time.Second}
	url := fmt.Sprintf(
		"https://api.cloudflare.com/client/v4/accounts/%s/d1/database/%s/time_travel/restore",
		accountID, databaseID,
	)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("D1 Time Travel request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("D1 Time Travel HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var result struct {
		Result struct {
			Bookmark  string `json:"bookmark"`
			Timestamp string `json:"timestamp"`
		} `json:"result"`
		Success bool      `json:"success"`
		Errors  []d1Error `json:"errors"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decoding Time Travel response: %w", err)
	}
	if !result.Success {
		msgs := make([]string, len(result.Errors))
		for i, e := range result.Errors {
			msgs[i] = fmt.Sprintf("[%d] %s", e.Code, e.Message)
		}
		return nil, fmt.Errorf("D1 Time Travel error: %s", strings.Join(msgs, "; "))
	}

	return &D1TimeTravelResult{
		Bookmark:  result.Result.Bookmark,
		Timestamp: result.Result.Timestamp,
	}, nil
}

// D1CreateDatabase creates a new D1 database in the account.
func D1CreateDatabase(name, location string) (*D1DatabaseInfo, error) {
	token := os.Getenv("CLOUDFLARE_API_TOKEN")
	accountID := os.Getenv("CLOUDFLARE_ACCOUNT_ID")
	if token == "" || accountID == "" {
		return nil, fmt.Errorf("CLOUDFLARE_API_TOKEN and CLOUDFLARE_ACCOUNT_ID must be set")
	}

	type createReq struct {
		Name            string `json:"name"`
		PrimaryLocation string `json:"primary_location_hint,omitempty"`
	}
	body, err := json.Marshal(createReq{Name: name, PrimaryLocation: location})
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/d1/database", accountID)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("D1 create request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("D1 create HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var result struct {
		Result struct {
			UUID      string `json:"uuid"`
			Name      string `json:"name"`
			CreatedAt string `json:"created_at"`
		} `json:"result"`
		Success bool      `json:"success"`
		Errors  []d1Error `json:"errors"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decoding D1 create response: %w", err)
	}
	if !result.Success {
		msgs := make([]string, len(result.Errors))
		for i, e := range result.Errors {
			msgs[i] = fmt.Sprintf("[%d] %s", e.Code, e.Message)
		}
		return nil, fmt.Errorf("D1 create error: %s", strings.Join(msgs, "; "))
	}

	return &D1DatabaseInfo{
		UUID:      result.Result.UUID,
		Name:      result.Result.Name,
		CreatedAt: result.Result.CreatedAt,
		DSN:       fmt.Sprintf("d1://%s", result.Result.UUID),
	}, nil
}

// D1DeleteDatabase deletes a D1 database permanently.
func D1DeleteDatabase(databaseID string) error {
	token := os.Getenv("CLOUDFLARE_API_TOKEN")
	accountID := os.Getenv("CLOUDFLARE_ACCOUNT_ID")
	if token == "" || accountID == "" {
		return fmt.Errorf("CLOUDFLARE_API_TOKEN and CLOUDFLARE_ACCOUNT_ID must be set")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	url := fmt.Sprintf(
		"https://api.cloudflare.com/client/v4/accounts/%s/d1/database/%s",
		accountID, databaseID,
	)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("D1 delete request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("D1 delete HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var result struct {
		Success bool      `json:"success"`
		Errors  []d1Error `json:"errors"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decoding D1 delete response: %w", err)
	}
	if !result.Success {
		msgs := make([]string, len(result.Errors))
		for i, e := range result.Errors {
			msgs[i] = fmt.Sprintf("[%d] %s", e.Code, e.Message)
		}
		return fmt.Errorf("D1 delete error: %s", strings.Join(msgs, "; "))
	}
	return nil
}

// ListD1Databases returns all D1 databases for the account using credentials
// from the environment (CLOUDFLARE_API_TOKEN + CLOUDFLARE_ACCOUNT_ID).
func ListD1Databases() ([]D1DatabaseInfo, error) {
	token := os.Getenv("CLOUDFLARE_API_TOKEN")
	accountID := os.Getenv("CLOUDFLARE_ACCOUNT_ID")
	if token == "" || accountID == "" {
		return nil, fmt.Errorf("CLOUDFLARE_API_TOKEN and CLOUDFLARE_ACCOUNT_ID must be set")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/d1/database", accountID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("D1 list request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("D1 HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var result struct {
		Result []struct {
			UUID      string `json:"uuid"`
			Name      string `json:"name"`
			CreatedAt string `json:"created_at"`
			NumTables int    `json:"num_tables"`
		} `json:"result"`
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decoding D1 list response: %w", err)
	}
	if !result.Success {
		return nil, fmt.Errorf("D1 list returned success=false")
	}

	dbs := make([]D1DatabaseInfo, len(result.Result))
	for i, r := range result.Result {
		dbs[i] = D1DatabaseInfo{
			UUID:      r.UUID,
			Name:      r.Name,
			CreatedAt: r.CreatedAt,
			NumTables: r.NumTables,
			DSN:       fmt.Sprintf("d1://%s", r.UUID),
		}
	}
	return dbs, nil
}

// D1DatabaseInfo describes one D1 database in an account.
type D1DatabaseInfo struct {
	UUID      string `json:"uuid"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	NumTables int    `json:"num_tables"`
	DSN       string `json:"dsn"`
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

func (d *d1Connector) QueryRows(query string) ([]map[string]interface{}, error) {
	return d.execute(query)
}

func (d *d1Connector) Capabilities() ExecCapabilities {
	// D1's HTTP API auto-commits each request and has no interactive
	// transactions, so a multi-statement migration cannot be rolled back as a
	// unit over REST. Use Cloudflare's Time Travel for point-in-time recovery.
	return ExecCapabilities{Transactional: false, LocalBackup: false, Provider: "d1"}
}

// Exec runs statements sequentially, stopping on the first error. D1 over HTTP
// has no interactive transaction, so already-applied statements are NOT rolled
// back; the returned error reports how many statements committed before failing.
func (d *d1Connector) Exec(statements []string, dryRun bool) error {
	if dryRun {
		return fmt.Errorf("D1 does not support --dry-run: its HTTP API has no rollback")
	}
	for i, stmt := range statements {
		if _, err := d.execute(stmt); err != nil {
			return fmt.Errorf("statement %d failed (%d already committed, NOT rolled back): %w",
				i+1, i, err)
		}
	}
	return nil
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
