package dev

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

const dexUsers = `users:
  - id: jdoe
    attributes: {email: jdoe@example.com, name: John Doe}
    groups: [g]
  - id: ops
    password: s3cret
    groups: []
`

// The stack's Dex logs dev.yaml's users in, their password their own or
// their id; Kayra has it as a login provider, and its authorization server
// on. A render again keeps the hashes: nothing changes.
func TestDex(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, dexUsers))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), ".infrapi-dev")
	if _, err := Render(c, dir, RenderOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	var dex struct {
		Issuer          string           `yaml:"issuer"`
		StaticClients   []map[string]any `yaml:"staticClients"`
		StaticPasswords []dexPassword    `yaml:"staticPasswords"`
	}
	if err := yaml.Unmarshal([]byte(read(t, filepath.Join(dir, dexConfig))), &dex); err != nil {
		t.Fatal(err)
	}
	if dex.Issuer != "http://localhost:9080/dex" || len(dex.StaticClients) != 1 || dex.StaticClients[0]["id"] != DexClient ||
		dex.StaticClients[0]["redirectURIs"].([]any)[0] != "http://localhost:8081/oauth2/callback" {
		t.Fatalf("dex: %+v", dex)
	}
	want := map[string][2]string{"jdoe": {"jdoe@example.com", "jdoe"}, "ops": {"ops@example.com", "s3cret"}}
	for _, p := range dex.StaticPasswords {
		w := want[p.UserID]
		if p.Email != w[0] || p.PreferredUsername != p.UserID || bcrypt.CompareHashAndPassword([]byte(p.Hash), []byte(w[1])) != nil {
			t.Errorf("dex user %+v, want %v", p, w)
		}
	}
	kayra := read(t, filepath.Join(dir, "kayra.yaml"))
	for _, s := range []string{"url: \"http://localhost:9080/dex/.well-known/openid-configuration\"", "proxy: \"http://apisix:9080\"",
		"client_id: kayra-dev", "field: preferred_username", "oauth2:\n  enabled: true"} {
		if !strings.Contains(kayra, s) {
			t.Errorf("kayra.yaml has no %q:\n%s", s, kayra)
		}
	}
	if !strings.Contains(read(t, filepath.Join(dir, "compose.yaml")), "dex:") {
		t.Error("compose.yaml has no dex")
	}
	if _, err := os.Stat(filepath.Join(dir, "routes", "05-dex.json")); err != nil {
		t.Error(err)
	}
	if r, err := Render(c, dir, RenderOptions{}); err != nil || len(r.Files) != 0 {
		t.Fatalf("rendered again: %v, %v", r.Files, err)
	}
	// a password changed: a new hash
	c.Users[0].Password = "other"
	if r, _ := Render(c, dir, RenderOptions{}); !slices.Contains(r.Files, dexConfig) || !r.Kayra {
		t.Fatalf("a new password changed %v", r.Files)
	}
}

// idp.enabled: false is no Dex: no service, no route, no login provider.
func TestDex_disabled(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, dexUsers+"idp: {enabled: false}\n"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), ".infrapi-dev")
	if _, err := Render(c, dir, RenderOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, dexConfig)); err == nil {
		t.Error("dex/config.yaml rendered")
	}
	if _, err := os.Stat(filepath.Join(dir, "routes", "05-dex.json")); err == nil {
		t.Error("dex's route rendered")
	}
	var compose struct {
		Services map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(read(t, filepath.Join(dir, "compose.yaml"))), &compose); err != nil {
		t.Fatal(err)
	}
	if _, ok := compose.Services["dex"]; ok || compose.Services["kayra"] == nil {
		t.Errorf("services: %v", compose.Services)
	}
	if k := read(t, filepath.Join(dir, "kayra.yaml")); strings.Contains(k, "dex") || strings.Contains(k, "\noauth2:") {
		t.Errorf("kayra.yaml:\n%s", k)
	}
}
