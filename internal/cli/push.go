package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/croc100/litescope/internal/fleet"
	"github.com/spf13/cobra"
)

// pushDatabase is one database's metadata, mirroring the cloud `/v1/push`
// contract. It carries metadata only — never customer rows.
type pushDatabase struct {
	Name        string      `json:"name"`
	Tags        []string    `json:"tags,omitempty"`
	Severity    string      `json:"severity,omitempty"`
	Report      interface{} `json:"report,omitempty"`
	Fingerprint string      `json:"fingerprint,omitempty"`
	Drift       interface{} `json:"drift,omitempty"`
}

type pushBody struct {
	Databases []pushDatabase `json:"databases"`
}

type pushResponse struct {
	OK       bool `json:"ok"`
	Accepted int  `json:"accepted"`
	Capped   int  `json:"capped"`
	Cap      int  `json:"cap"`
}

func cmdPush() *cobra.Command {
	var configPath string
	var tag string
	var deep bool
	var endpoint string
	var key string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push fleet health + schema fingerprints to the hosted dashboard (Enterprise)",
		Long: `Collect each database's operational health and schema fingerprint and send
the metadata to your hosted Litescope dashboard (Enterprise).

Only metadata is transmitted — severity, health report, schema fingerprint, and
optional drift. Customer rows never leave this machine. Run it on a schedule
(cron/systemd timer) to keep the hosted dashboard current.

Configuration:
  --endpoint   hosted dashboard base URL (env LITESCOPE_CLOUD_URL)
  --key        API key, lsk_…           (env LITESCOPE_CLOUD_KEY)

Examples:
  litescope push
  litescope push --config litescope.fleet.yaml --tag region=eu
  litescope push --dry-run        # print the payload without sending`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if endpoint == "" {
				endpoint = os.Getenv("LITESCOPE_CLOUD_URL")
			}
			if key == "" {
				key = os.Getenv("LITESCOPE_CLOUD_KEY")
			}

			_, dbs, err := loadFleet(configPath, tag)
			if err != nil {
				return err
			}
			if len(dbs) == 0 {
				return fmt.Errorf("no databases to push (check --config / --tag)")
			}

			body := buildPushBody(dbs, deep)

			if dryRun {
				out, _ := json.MarshalIndent(body, "", "  ")
				fmt.Println(string(out))
				fmt.Printf("\n  %s  Dry run — %d database(s) prepared, nothing sent.\n",
					styleDim.Render("·"), len(body.Databases))
				return nil
			}

			if endpoint == "" {
				return fmt.Errorf("no endpoint set — use --endpoint or LITESCOPE_CLOUD_URL")
			}
			if key == "" {
				return fmt.Errorf("no API key set — use --key or LITESCOPE_CLOUD_KEY")
			}

			resp, err := sendPush(endpoint, key, body)
			if err != nil {
				return err
			}

			fmt.Printf("\n  %s  Pushed %d database(s) to %s\n",
				styleOK.Render("◎"), resp.Accepted, trimEndpoint(endpoint))
			if resp.Capped > 0 {
				fmt.Printf("  %s  %s\n", styleWarn.Render("!"),
					styleWarn.Render(fmt.Sprintf("%d database(s) skipped — plan cap is %d. Upgrade for more.", resp.Capped, resp.Cap)))
			}
			fmt.Println()
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "", "fleet config file (default litescope.fleet.yaml)")
	cmd.Flags().StringVar(&tag, "tag", "", "only include databases matching tag (key=value or key)")
	cmd.Flags().BoolVar(&deep, "deep", false, "run exhaustive integrity_check on each database")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "hosted dashboard base URL (env LITESCOPE_CLOUD_URL)")
	cmd.Flags().StringVar(&key, "key", "", "API key lsk_… (env LITESCOPE_CLOUD_KEY)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the payload without sending")
	return cmd
}

// buildPushBody runs health + fingerprint over the fleet and assembles the
// metadata-only payload the cloud expects.
func buildPushBody(dbs []fleet.Database, deep bool) pushBody {
	health := fleet.Health(dbs, deep, 0)
	fp := fleet.Fingerprint(dbs, 0)

	// Index health reports by database name.
	type hinfo struct {
		severity string
		report   interface{}
	}
	healthByName := make(map[string]hinfo, len(health.Results))
	for _, res := range health.Results {
		h := hinfo{}
		if res.Report != nil {
			h.severity = res.Report.Severity.String()
			h.report = res.Report
		}
		healthByName[res.Database] = h
	}

	// Index fingerprint (and drift) by database name via cluster membership.
	type finfo struct {
		fingerprint string
		drift       interface{}
	}
	fpByName := make(map[string]finfo)
	for _, cluster := range fp.Clusters {
		for _, member := range cluster.Members {
			f := finfo{fingerprint: cluster.ID}
			if !cluster.IsCanonical && len(cluster.Drift) > 0 {
				f.drift = cluster.Drift
			}
			fpByName[member] = f
		}
	}

	out := pushBody{Databases: make([]pushDatabase, 0, len(dbs))}
	for _, db := range dbs {
		pd := pushDatabase{Name: db.Name, Tags: db.Tags}
		if h, ok := healthByName[db.Name]; ok {
			pd.Severity = h.severity
			pd.Report = h.report
		}
		if f, ok := fpByName[db.Name]; ok {
			pd.Fingerprint = f.fingerprint
			pd.Drift = f.drift
		}
		out.Databases = append(out.Databases, pd)
	}
	return out
}

// sendPush POSTs the payload to <endpoint>/v1/push with a Bearer key.
func sendPush(endpoint, key string, body pushBody) (*pushResponse, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	url := strings.TrimRight(endpoint, "/") + "/v1/push"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("push failed: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("push rejected (401) — check your API key")
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("push returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var pr pushResponse
	if err := json.Unmarshal(raw, &pr); err != nil {
		return nil, fmt.Errorf("unexpected response from server: %s", strings.TrimSpace(string(raw)))
	}
	return &pr, nil
}

// trimEndpoint shortens a URL for display (drops scheme).
func trimEndpoint(endpoint string) string {
	s := strings.TrimPrefix(endpoint, "https://")
	s = strings.TrimPrefix(s, "http://")
	return strings.TrimRight(s, "/")
}
