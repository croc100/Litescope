package monitor

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/croc100/litescope/internal/schema"
)

// Snapshot is a point-in-time record of a database schema.
type Snapshot struct {
	Version   int            `json:"version"`
	Source    string         `json:"source"`
	CapturedAt time.Time    `json:"captured_at"`
	Schema    *schema.Schema `json:"schema"`
}

const snapshotVersion = 1

// Save writes a snapshot to disk.
func Save(path string, snap *Snapshot) error {
	snap.Version = snapshotVersion
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling snapshot: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// Load reads a snapshot from disk.
func Load(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading snapshot %s: %w", path, err)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("parsing snapshot: %w", err)
	}
	return &snap, nil
}
