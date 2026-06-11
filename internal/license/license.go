// Package license handles Pro/Cloud feature gating.
// License key lookup order:
//  1. LITESCOPE_LICENSE env var
//  2. ~/.litescope/license file
package license

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Tier int

const (
	TierFree  Tier = 0
	TierPro   Tier = 1
	TierCloud Tier = 2
)

// Current returns the active license tier based on environment.
func Current() Tier {
	key := resolveKey()
	if key == "" {
		return TierFree
	}
	// Cloud keys start with "lsc_cloud_", Pro with "lsc_pro_"
	// In production this would validate against Litescope API.
	switch {
	case strings.HasPrefix(key, "lsc_cloud_"):
		return TierCloud
	case strings.HasPrefix(key, "lsc_pro_"):
		return TierPro
	default:
		return TierFree
	}
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

// RequirePro checks for Pro or Cloud tier.
// Returns a descriptive error with upgrade URL if not met.
func RequirePro() error {
	if Current() >= TierPro {
		return nil
	}
	return fmt.Errorf(`%w

  This feature requires Litescope Pro.

  Upgrade at: https://github.com/croc100/Litescope#pricing
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

  Upgrade at: https://github.com/croc100/Litescope#pricing
  Then set:   export LITESCOPE_LICENSE=lsc_cloud_<your-key>

  Cloud ($49/mo): hosted monitoring, team dashboard, audit trail`,
		ErrUpgradeRequired)
}

var ErrUpgradeRequired = fmt.Errorf("upgrade required")
