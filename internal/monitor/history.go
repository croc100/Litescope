package monitor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// HistoryEntry is one line from a JSONL report file.
type HistoryEntry struct {
	*DriftResult
}

// LoadHistory reads all entries from a JSONL report file, sorted by CheckedAt ascending.
func LoadHistory(path string) ([]HistoryEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening report %s: %w", path, err)
	}
	defer f.Close()

	var entries []HistoryEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024) // 10MB max line — large drift payloads
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var r DriftResult
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("parsing report line: %w", err)
		}
		entries = append(entries, HistoryEntry{&r})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].CheckedAt.Before(entries[j].CheckedAt)
	})

	return entries, nil
}

// HistorySummary aggregates stats across all history entries.
type HistorySummary struct {
	Source      string
	TotalChecks int
	DriftCount  int
	LastChecked string
	LastDrift   string
}

func Summarize(entries []HistoryEntry) HistorySummary {
	if len(entries) == 0 {
		return HistorySummary{}
	}

	s := HistorySummary{
		Source:      entries[0].Source,
		TotalChecks: len(entries),
		LastChecked: entries[len(entries)-1].CheckedAt.Format("2006-01-02 15:04 UTC"),
	}
	for _, e := range entries {
		if e.HasDrift {
			s.DriftCount++
			s.LastDrift = e.CheckedAt.Format("2006-01-02 15:04 UTC")
		}
	}
	return s
}
