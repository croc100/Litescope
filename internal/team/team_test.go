package team

import (
	"os"
	"path/filepath"
	"testing"
)

func cfg() *Config {
	return &Config{Team: "Acme", Members: []Member{
		{Name: "alice", Role: "admin"},
		{Name: "bob", Role: "viewer"},
		{Name: "carol", Role: "editor"},
	}}
}

func TestCanWrite(t *testing.T) {
	c := cfg()
	if ok, _ := c.CanWrite("alice"); !ok {
		t.Fatal("admin should write")
	}
	if ok, _ := c.CanWrite("carol"); !ok {
		t.Fatal("editor should write")
	}
	if ok, reason := c.CanWrite("bob"); ok || reason == "" {
		t.Fatalf("viewer should be blocked, got ok=%v reason=%q", ok, reason)
	}
	// unknown operator: lenient by default
	if ok, _ := c.CanWrite("dave"); !ok {
		t.Fatal("unknown operator should be allowed when not strict")
	}
	// case-insensitive name match
	if ok, _ := c.CanWrite("BOB"); ok {
		t.Fatal("viewer match should be case-insensitive")
	}
}

func TestStrict(t *testing.T) {
	c := cfg()
	c.Strict = true
	if ok, _ := c.CanWrite("dave"); ok {
		t.Fatal("strict mode should block unknown operator")
	}
	if ok, _ := c.CanWrite("alice"); !ok {
		t.Fatal("strict mode should still allow admin")
	}
}

func TestEmptyAllowsAll(t *testing.T) {
	var nilC *Config
	if ok, _ := nilC.CanWrite("anyone"); !ok {
		t.Fatal("nil config should allow")
	}
	if ok, _ := (&Config{}).CanWrite("anyone"); !ok {
		t.Fatal("empty config should allow")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "team.yaml")
	os.WriteFile(path, []byte("team: Acme\nmembers:\n  - name: bob\n    role: viewer\n"), 0644)
	t.Setenv("LITESCOPE_TEAM", path)

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Team != "Acme" || c.Role("bob") != "viewer" {
		t.Fatalf("unexpected: %+v", c)
	}
}
