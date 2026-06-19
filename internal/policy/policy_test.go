package policy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAllow(t *testing.T) {
	var nilP *Policy
	if err := nilP.Allow("anything"); err != nil {
		t.Fatalf("nil policy should allow: %v", err)
	}

	ro := &Policy{ReadOnly: true}
	if err := ro.Allow("x.db"); err == nil {
		t.Fatal("read-only should block")
	}

	prot := &Policy{Protected: []string{"prod", "/data/critical"}}
	if err := prot.Allow("/srv/prod-01.db"); err == nil {
		t.Fatal("protected substring should block")
	}
	if err := prot.Allow("/srv/staging.db"); err != nil {
		t.Fatalf("non-protected should allow: %v", err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.yaml")
	os.WriteFile(path, []byte("read_only: true\nprotected:\n  - prod\n"), 0644)
	t.Setenv("LITESCOPE_POLICY", path)

	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !p.ReadOnly || len(p.Protected) != 1 || p.Protected[0] != "prod" {
		t.Fatalf("unexpected policy: %+v", p)
	}
	if p.Source() != path {
		t.Fatalf("source not set: %s", p.Source())
	}
}

func TestLoadMissingIsEmpty(t *testing.T) {
	t.Setenv("LITESCOPE_POLICY", filepath.Join(t.TempDir(), "nope.yaml"))
	// also avoid picking up a real ~/.litescope/policy.yaml or ./litescope.policy.yaml
	dir := t.TempDir()
	old, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(old)
	t.Setenv("HOME", dir)

	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !p.Empty() {
		t.Fatalf("expected empty policy, got %+v", p)
	}
}
