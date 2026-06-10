package render

import (
	"encoding/json"
	"io"

	"github.com/croc100/litescope/internal/diff"
)

func JSON(w io.Writer, r *diff.Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
