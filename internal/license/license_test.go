package license

import (
	"errors"
	"testing"
)

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

func TestCurrent_Cloud(t *testing.T) {
	t.Setenv("LITESCOPE_LICENSE", "lsc_cloud_xyz789")
	if got := Current(); got != TierCloud {
		t.Errorf("expected TierCloud, got %v", got)
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

func TestRequirePro_CloudAllowed(t *testing.T) {
	t.Setenv("LITESCOPE_LICENSE", "lsc_cloud_key")
	if err := RequirePro(); err != nil {
		t.Errorf("Cloud key should satisfy RequirePro, got: %v", err)
	}
}

func TestRequireCloud_Blocked(t *testing.T) {
	t.Setenv("LITESCOPE_LICENSE", "lsc_pro_key")
	err := RequireCloud()
	if err == nil {
		t.Fatal("Pro key should not satisfy RequireCloud")
	}
	if !errors.Is(err, ErrUpgradeRequired) {
		t.Errorf("expected ErrUpgradeRequired, got %v", err)
	}
}

func TestRequireCloud_Allowed(t *testing.T) {
	t.Setenv("LITESCOPE_LICENSE", "lsc_cloud_key")
	if err := RequireCloud(); err != nil {
		t.Errorf("Cloud key should pass RequireCloud, got: %v", err)
	}
}
