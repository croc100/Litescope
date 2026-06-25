package license

import (
	"os"
	"testing"
)

func init() {
	// Skip online verification in tests — test keys are not registered in the worker.
	_ = os.Setenv("LITESCOPE_SKIP_VERIFY", "1")
}

func TestCurrent_Free(t *testing.T) {
	t.Setenv("LITESCOPE_LICENSE", "")
	if got := Current(); got != TierFree {
		t.Errorf("expected TierFree, got %v", got)
	}
}

func TestCurrent_Pro(t *testing.T) {
	t.Setenv("LITESCOPE_LICENSE", "lsc_pro_abc123")
	if got := Current(); got != TierPro {
		t.Errorf("expected TierPro, got %v", got)
	}
}

func TestCurrent_UnknownKey(t *testing.T) {
	t.Setenv("LITESCOPE_LICENSE", "lsc_unknown_key")
	if got := Current(); got != TierFree {
		t.Errorf("unknown prefix should resolve to TierFree, got %v", got)
	}
}

// Litescope is now fully free (AGPL-3.0): RequirePro is a no-op and IsPro is
// always true, so no feature is ever gated regardless of license state.
func TestRequirePro_NeverBlocks(t *testing.T) {
	t.Setenv("LITESCOPE_LICENSE", "")
	if err := RequirePro(); err != nil {
		t.Errorf("the free CLI must never block: %v", err)
	}
}

func TestIsPro_AlwaysTrue(t *testing.T) {
	t.Setenv("LITESCOPE_LICENSE", "")
	if !IsPro() {
		t.Error("IsPro must be true for the fully-free CLI")
	}
}
