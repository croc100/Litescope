package fleet

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

// DiscoverTurso lists every database in a Turso organization via the platform API
// and returns fleet entries with ready-to-use turso:// DSNs.
//
// platformToken is an org/platform API token (https://api.turso.tech).
// dbToken is the database auth token applied to each entry's DSN; a group-level
// auth token works across the whole org. When dbToken is empty, the DSN is
// emitted with a TOKEN placeholder for the user to fill in.
func DiscoverTurso(org, platformToken, dbToken string) ([]Database, error) {
	url := fmt.Sprintf("https://api.turso.tech/v1/organizations/%s/databases", org)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+platformToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("turso API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("turso API HTTP %d: %s", resp.StatusCode, string(body))
	}

	var out struct {
		Databases []struct {
			Name     string `json:"Name"`
			Hostname string `json:"Hostname"`
			Group    string `json:"group"`
		} `json:"databases"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decoding turso response: %w", err)
	}

	token := dbToken
	if token == "" {
		token = "TOKEN"
	}

	var dbs []Database
	for _, d := range out.Databases {
		entry := Database{
			Name: d.Name,
			DSN:  fmt.Sprintf("turso://%s@%s/%s", token, org, d.Name),
		}
		if d.Group != "" {
			entry.Tags = []string{"group:" + d.Group}
		}
		dbs = append(dbs, entry)
	}
	sort.Slice(dbs, func(i, j int) bool { return dbs[i].Name < dbs[j].Name })
	return dbs, nil
}

// DiscoverD1 lists every D1 database in a Cloudflare account and returns fleet
// entries with d1:// DSNs. The same API token is used for discovery and queries,
// so the returned DSNs are immediately usable.
func DiscoverD1(accountID, token string) ([]Database, error) {
	var dbs []Database
	page := 1

	for {
		url := fmt.Sprintf(
			"https://api.cloudflare.com/client/v4/accounts/%s/d1/database?per_page=100&page=%d",
			accountID, page,
		)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("cloudflare API request failed: %w", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("cloudflare API HTTP %d: %s", resp.StatusCode, string(body))
		}

		var out struct {
			Success bool `json:"success"`
			Result  []struct {
				UUID string `json:"uuid"`
				Name string `json:"name"`
			} `json:"result"`
			ResultInfo struct {
				Page       int `json:"page"`
				TotalPages int `json:"total_pages"`
			} `json:"result_info"`
			Errors []struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("decoding cloudflare response: %w", err)
		}
		if !out.Success {
			if len(out.Errors) > 0 {
				return nil, fmt.Errorf("cloudflare API error [%d]: %s", out.Errors[0].Code, out.Errors[0].Message)
			}
			return nil, fmt.Errorf("cloudflare API returned success=false")
		}

		for _, d := range out.Result {
			dbs = append(dbs, Database{
				Name: d.Name,
				DSN:  fmt.Sprintf("d1://%s@%s/%s", token, accountID, d.UUID),
			})
		}

		if out.ResultInfo.TotalPages <= page || len(out.Result) == 0 {
			break
		}
		page++
	}

	sort.Slice(dbs, func(i, j int) bool { return dbs[i].Name < dbs[j].Name })
	return dbs, nil
}
