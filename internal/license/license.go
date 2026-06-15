// Package license handles Pro/Cloud feature gating.
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
	TierFree  Tier = 0
	TierPro   Tier = 1
	TierCloud Tier = 2
)

const workerURL = "https://litescope-license-worker.croc100.workers.dev/verify"

// Current returns the active license tier, verified against the license server.
// Falls back to local prefix check only if the server is unreachable (offline grace).
func Current() Tier {
	key := resolveKey()
	if key == "" {
		return TierFree
	}
	if tier, ok := verifyOnline(key); ok {
		return tier
	}
	// Offline grace: trust prefix locally if server unreachable
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
		// Network unreachable — offline grace
		return 0, false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Key not in database — invalid
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
	switch v.Tier {
	case "cloud":
		return TierCloud, true
	case "pro":
		return TierPro, true
	default:
		return TierFree, true
	}
}

func tierFromPrefix(key string) Tier {
	switch {
	case strings.HasPrefix(key, "lsc_cloud_"):
		return TierCloud
	case strings.HasPrefix(key, "lsc_pro_"):
		return TierPro
	default:
		return TierFree
	}
}

// RequirePro checks for Pro or Cloud tier.
func RequirePro() error {
	if Current() >= TierPro {
		return nil
	}
	return fmt.Errorf(`%w

  This feature requires Litescope Pro.

  Upgrade at: https://croc100.github.io/Litescope/#pricing
  Then set:   export LITESCOPE_LICENSE=lsc_pro_<your-key>

  Pro ($9/mo): continuous monitoring, webhook alerts, CI reports`,
		ErrUpgradeRequired)
}

// RequireCloud checks for Cloud tier.
func RequireCloud() error {
	if Current() >= TierCloud {
		return nil
	}
	return fmt.Errorf(`%w

  This feature requires Litescope Cloud.

  Upgrade at: https://croc100.github.io/Litescope/#pricing
  Then set:   export LITESCOPE_LICENSE=lsc_cloud_<your-key>

  Cloud ($49/mo): hosted monitoring, team dashboard, audit trail`,
		ErrUpgradeRequired)
}

var ErrUpgradeRequired = fmt.Errorf("upgrade required")
