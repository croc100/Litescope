package monitor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/schema"
)

// DriftResult is the outcome of comparing a live schema against a baseline snapshot.
type DriftResult struct {
	Source     string         `json:"source"`
	BaselineAt time.Time      `json:"baseline_at"`
	CheckedAt  time.Time      `json:"checked_at"`
	HasDrift   bool           `json:"has_drift"`
	Changes    []diff.TableDiff `json:"changes,omitempty"`
}

// Check compares currentSchema against the snapshot baseline.
func Check(source string, snap *Snapshot, current *schema.Schema) *DriftResult {
	result := diff.CompareSchemas(snap.Schema, current)
	return &DriftResult{
		Source:     source,
		BaselineAt: snap.CapturedAt,
		CheckedAt:  time.Now().UTC(),
		HasDrift:   len(result.Schema) > 0,
		Changes:    result.Schema,
	}
}

// Alert sends a drift result to a webhook URL (Pro feature).
// Supports Slack Block Kit (when URL contains "hooks.slack.com") and generic JSON.
func Alert(webhookURL string, result *DriftResult) error {
	if !result.HasDrift {
		return nil
	}

	var payload []byte
	var err error

	if strings.Contains(webhookURL, "hooks.slack.com") || strings.Contains(webhookURL, "slack") {
		payload, err = json.Marshal(slackPayload(result))
	} else {
		payload, err = json.Marshal(genericPayload(result))
	}
	if err != nil {
		return err
	}

	resp, err := http.Post(webhookURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("webhook failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// AppendReport appends a DriftResult as a single JSON line to a JSONL file.
// Creates the file and parent directories if they don't exist.
func AppendReport(path string, result *DriftResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating report directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening report file: %w", err)
	}
	defer f.Close()

	line, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "%s\n", line)
	return err
}

func slackPayload(r *DriftResult) map[string]interface{} {
	lines := []string{}
	for _, td := range r.Changes {
		switch {
		case td.Added:
			lines = append(lines, fmt.Sprintf("• `%s` *added*", td.Name))
		case td.Removed:
			lines = append(lines, fmt.Sprintf("• `%s` *removed*", td.Name))
		default:
			cols := len(td.AddedColumns) + len(td.RemovedColumns) + len(td.ChangedColumns)
			idxs := len(td.AddedIndexes) + len(td.RemovedIndexes)
			detail := ""
			if cols > 0 {
				detail += fmt.Sprintf("%d column change(s)", cols)
			}
			if idxs > 0 {
				if detail != "" {
					detail += ", "
				}
				detail += fmt.Sprintf("%d index change(s)", idxs)
			}
			lines = append(lines, fmt.Sprintf("• `%s` modified — %s", td.Name, detail))
		}
	}

	body := strings.Join(lines, "\n")
	if body == "" {
		body = "(no details)"
	}

	return map[string]interface{}{
		"text": fmt.Sprintf("⚠️ Schema drift detected in `%s`", r.Source),
		"blocks": []map[string]interface{}{
			{
				"type": "header",
				"text": map[string]string{
					"type": "plain_text",
					"text": "⚠️ Schema Drift Detected",
				},
			},
			{
				"type": "section",
				"fields": []map[string]string{
					{"type": "mrkdwn", "text": fmt.Sprintf("*Source*\n`%s`", r.Source)},
					{"type": "mrkdwn", "text": fmt.Sprintf("*Changes*\n%d table(s)", len(r.Changes))},
					{"type": "mrkdwn", "text": fmt.Sprintf("*Baseline*\n%s", r.BaselineAt.Format("2006-01-02 15:04 UTC"))},
					{"type": "mrkdwn", "text": fmt.Sprintf("*Detected*\n%s", r.CheckedAt.Format("2006-01-02 15:04 UTC"))},
				},
			},
			{
				"type": "section",
				"text": map[string]string{
					"type": "mrkdwn",
					"text": body,
				},
			},
			{
				"type": "context",
				"elements": []map[string]string{
					{"type": "mrkdwn", "text": "Sent by <https://github.com/croc100/Litescope|Litescope> · Pro"},
				},
			},
		},
	}
}

func genericPayload(r *DriftResult) map[string]interface{} {
	return map[string]interface{}{
		"text":    fmt.Sprintf("Schema drift detected in %s (%d changes)", r.Source, len(r.Changes)),
		"result":  r,
	}
}
