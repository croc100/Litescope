package monitor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
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
func Alert(webhookURL string, result *DriftResult) error {
	if !result.HasDrift {
		return nil
	}

	payload, err := json.Marshal(map[string]interface{}{
		"text":    fmt.Sprintf("⚠️ Schema drift detected in %s (%d changes)", result.Source, len(result.Changes)),
		"result":  result,
	})
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
