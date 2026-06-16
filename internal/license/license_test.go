package license

import (
	"errors"
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

func TestRequirePro_Blocked(t *testing.T) {
	t.Setenv("LITESCOPE_LICENSE", "")
	err := RequirePro()
	if err == nil {
		t.Fatal("expected error for free tier")
	}
	if !errors.Is(err, ErrUpgradeRequired) {
		t.Errorf("expected ErrUpgradeRequired, got %v", err)
	}
}

func TestRequirePro_Allowed(t *testing.T) {
	t.Setenv("LITESCOPE_LICENSE", "lsc_pro_key")
	if err := RequirePro(); err != nil {
		t.Errorf("Pro key should pass RequirePro, got: %v", err)
	}
}
