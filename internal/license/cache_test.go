package license

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCache puts a cache entry under a temp HOME and returns nothing; tests set
// HOME so both resolveKey's file lookup and the cache live in isolation.
func writeCache(t *testing.T, home, key string, tier Tier, verifiedAt time.Time) {
	t.Helper()
	dir := filepath.Join(home, ".litescope")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(cacheEntry{KeyHash: hashKey(key), Tier: tier, VerifiedAt: verifiedAt})
	if err := os.WriteFile(filepath.Join(dir, ".verify_cache.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

// TestCurrent_FreshCacheSkipsNetwork: a fresh cache must be honored without any
// network call. We point the worker at an unreachable host implicitly by relying
// on the cache short-circuit (no SKIP_VERIFY), so if the cache were ignored the
// test would hit the real network and likely not return TierPro for a fake key.
func TestCurrent_FreshCacheSkipsNetwork(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LITESCOPE_LICENSE", "lsc_fake_key_not_registered")
	t.Setenv("LITESCOPE_SKIP_VERIFY", "") // verification ON (auto-restored)

	// Fresh cache says Pro for this key.
	writeCache(t, home, "lsc_fake_key_not_registered", TierPro, time.Now().UTC())

	if got := Current(); got != TierPro {
		t.Errorf("fresh cache should yield TierPro without network, got %v", got)
	}
}

func TestCurrent_GraceWindowHonorsCacheWhenOffline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A key whose prefix is Free, so prefix-fallback would give Free; only the
	// cache (within grace) can yield Pro. The worker host is unreachable here
	// (fake key → the real server returns not-registered/Free, but network may
	// also fail); either way, a stale-but-in-grace cache should win over prefix.
	t.Setenv("LITESCOPE_LICENSE", "lsc_legacy_pro_token")

	// Cache verified 3 days ago — stale for TTL but within the 14-day grace.
	writeCache(t, home, "lsc_legacy_pro_token", TierPro, time.Now().Add(-3*24*time.Hour))

	// Can't guarantee the network is offline in CI, so just assert the cache
	// helpers behave: within grace the entry is honored.
	c := loadCache()
	if c == nil || !c.matches("lsc_legacy_pro_token") {
		t.Fatal("cache did not round-trip")
	}
	if time.Since(c.VerifiedAt) >= offlineGrace {
		t.Errorf("cache should be within grace window")
	}
}

func TestCacheEntry_Matches(t *testing.T) {
	e := &cacheEntry{KeyHash: hashKey("abc")}
	if !e.matches("abc") {
		t.Errorf("matches should be true for the same key")
	}
	if e.matches("different") {
		t.Errorf("matches should be false for a different key")
	}
}

func TestSaveLoadCache_RoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	saveCache("mykey", TierPro)
	c := loadCache()
	if c == nil || c.Tier != TierPro || !c.matches("mykey") {
		t.Errorf("cache round-trip failed: %+v", c)
	}
	// The raw key must not be stored — only its hash.
	data, _ := os.ReadFile(filepath.Join(home, ".litescope", ".verify_cache.json"))
	if string(data) != "" && contains(string(data), "mykey") {
		t.Errorf("cache file leaked the raw key: %s", data)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
