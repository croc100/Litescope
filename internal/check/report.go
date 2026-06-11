package check

import (
	"encoding/json"
	"fmt"
	"os"
)

// AppendReport appends a single check result to a JSONL report file.
func AppendReport(path string, result *Result) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening report file: %w", err)
	}
	defer f.Close()

	line, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encoding result: %w", err)
	}
	_, err = fmt.Fprintf(f, "%s\n", line)
	return err
}
