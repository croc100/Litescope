// Package license handles Pro feature gating.
// License key lookup order:
//  1. LITESCOPE_LICENSE env var
//  2. ~/.litescope/license file
package license

import (
	"crypto/sha256"
	"encoding/hex"
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
	TierFree       Tier = 0
	TierPro        Tier = 1
	TierEnterprise Tier = 2 // "Ex" — web SaaS / self-host, sold via contact sales
)

const workerURL = "https://litescope-license-worker.croc100.workers.dev/verify"

// Verification caching removes the license server as a single point of failure
// and keeps the CLI fast: a fresh cache skips the network entirely, and a recent
// cache keeps paying users working while the server is unreachable.
const (
	verifyCacheTTL = 24 * time.Hour      // skip the network if verified this recently
	offlineGrace   = 14 * 24 * time.Hour // honor the last known tier this long when offline
)

// Current returns the active license tier. Resolution order:
//  1. fresh verification cache (no network)
//  2. online verification (refreshes the cache)
//  3. recent cache within the offline-grace window (server unreachable)
//  4. local key-prefix trust (last resort)
func Current() Tier {
	key := resolveKey()
	if key == "" {
		return TierFree
	}
	if os.Getenv("LITESCOPE_SKIP_VERIFY") != "" {
		return tierFromPrefix(key)
	}

	cache := loadCache()
	if cache != nil && cache.matches(key) && time.Since(cache.VerifiedAt) < verifyCacheTTL {
		return cache.Tier // fast path — no network
	}

	if tier, ok := verifyOnline(key); ok {
		saveCache(key, tier)
		return tier
	}

	// Server unreachable: honor the last known tier within the grace window.
	if cache != nil && cache.matches(key) && time.Since(cache.VerifiedAt) < offlineGrace {
		return cache.Tier
	}
	// Last resort: trust the key prefix so a valid key isn't hard-blocked.
	return tierFromPrefix(key)
}

// ── verification cache ──────────────────────────────────────────────────────

type cacheEntry struct {
	KeyHash    string    `json:"key_hash"`
	Tier       Tier      `json:"tier"`
	VerifiedAt time.Time `json:"verified_at"`
}

func (e *cacheEntry) matches(key string) bool { return e.KeyHash == hashKey(key) }

func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func cacheFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".litescope", ".verify_cache.json")
}

func loadCache() *cacheEntry {
	path := cacheFilePath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var e cacheEntry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil
	}
	return &e
}

func saveCache(key string, tier Tier) {
	path := cacheFilePath()
	if path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	data, err := json.Marshal(cacheEntry{KeyHash: hashKey(key), Tier: tier, VerifiedAt: time.Now().UTC()})
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0600)
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
	switch v.Tier {
	case "enterprise":
		return TierEnterprise, true
	case "pro":
		return TierPro, true
	}
	return TierFree, true
}

func tierFromPrefix(key string) Tier {
	switch {
	case strings.HasPrefix(key, "lsc_ent_"):
		return TierEnterprise
	case strings.HasPrefix(key, "lsc_pro_"):
		return TierPro
	}
	return TierFree
}

// RequirePro checks for Pro tier (Enterprise satisfies it too).
func RequirePro() error {
	if Current() >= TierPro {
		return nil
	}
	return fmt.Errorf(`%w

  This feature requires Litescope Pro.

  Get your license: https://croc100.github.io/Litescope/#pricing
  Then activate:   export LITESCOPE_LICENSE=<your-key>

  Pro ($89/year): migrate apply, drift monitor, fleet ops, unlimited connections
  Need teams, a web dashboard, SSO or self-host? See Enterprise — croc100100@gmail.com`,
		ErrUpgradeRequired)
}

var ErrUpgradeRequired = fmt.Errorf("upgrade required")
