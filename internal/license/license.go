// Package license handles Pro feature gating.
// License key lookup order:
//  1. LITESCOPE_LICENSE env var
//  2. ~/.litescope/license file
package license

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Tier int

const (
	TierFree Tier = 0
	TierPro  Tier = 1
)

const workerURL = "https://litescope-license-worker.croc100.workers.dev/verify"

// Current returns the active license tier, verified against the license server.
// Falls back to local prefix check only if the server is unreachable (offline grace).
func Current() Tier {
	key := resolveKey()
	if key == "" {
		return TierFree
	}
	if os.Getenv("LITESCOPE_SKIP_VERIFY") == "" {
		if tier, ok := verifyOnline(key); ok {
			return tier
		}
	}
	// Offline grace (or LITESCOPE_SKIP_VERIFY set): trust prefix locally
	return tierFromPrefix(key)
}

func resolveKey() string {
	if v := os.Getenv("LITESCOPE_LICENSE"); v != "" {
		return strings.TrimSpace(v)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".litescope", "license"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

type verifyResponse struct {
	Valid bool   `json:"valid"`
	Tier  string `json:"tier"`
}

// verifyOnline calls the license worker. Returns (tier, true) on success,
// (0, false) if the server is unreachable so caller can fall back gracefully.
func verifyOnline(key string) (Tier, bool) {
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(workerURL + "?key=" + key)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return TierFree, true
	}
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}

	var v verifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return 0, false
	}
	if !v.Valid {
		return TierFree, true
	}
	if v.Tier == "pro" {
		return TierPro, true
	}
	return TierFree, true
}

func tierFromPrefix(key string) Tier {
	if strings.HasPrefix(key, "lsc_pro_") {
		return TierPro
	}
	return TierFree
}

// RequirePro checks for Pro tier.
func RequirePro() error {
	if Current() >= TierPro {
		return nil
	}
	return fmt.Errorf(`%w

  This feature requires Litescope Pro.

  Get your license: https://litescope-site.pages.dev/#pricing
  Then activate:   export LITESCOPE_LICENSE=<your-key>

  Pro ($89/year): drift monitor, fleet ops, unlimited connections`,
		ErrUpgradeRequired)
}

var ErrUpgradeRequired = fmt.Errorf("upgrade required")
