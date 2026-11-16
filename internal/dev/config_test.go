package dev

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "dev.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The dev.yaml the infrapi/dev template renders with its defaults loads.
func TestLoadConfig_template(t *testing.T) {
	c, err := LoadConfig("testdata/dev.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Domain != "infrapi.io" || len(c.Users) != 3 || c.FullAPI(c.Example.API) != "compute.infrapi.io" {
		t.Fatalf("dev.yaml: %+v", c)
	}
}

// An empty file is the defaults; a name without a dot is <name>.<domain>.
func TestLoadConfig_defaults(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, "domain: example.org\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Platform != "sandbox" || c.Ports.Gateway != 9080 || c.Kayra.Image != "ghcr.io/infrapi/kayra:latest" ||
		c.SysadminGroups[0] != "sandbox-core-sysadmin" || c.FullAPI(c.Example.API) != "compute.example.org" {
		t.Fatalf("defaults: %+v", c)
	}
	if got := c.FullAPI("compute"); got != "compute.example.org" {
		t.Errorf("FullAPI(compute) = %s", got)
	}
	if got := c.FullAPI("pki.other.net"); got != "pki.other.net" {
		t.Errorf("FullAPI(pki.other.net) = %s", got)
	}
	if got := c.Builtin("catalog"); got != "catalog.example.org" {
		t.Errorf("Builtin(catalog) = %s", got)
	}
}

func TestLoadConfig_refuses(t *testing.T) {
	for name, content := range map[string]string{
		"unknown key":       "domian: infrapi.io\n",
		"one label domain":  "domain: localhost\n",
		"unknown platform":  "platform: lab\n",
		"upper case domain": "domain: Infrapi.io\n",
		"same port twice":   "ports: {gateway: 8081}\n",
		"bad example api":   "example: {api: Compute}\n",
		"user twice":        "users: [{id: a}, {id: a}]\n",
	} {
		if _, err := LoadConfig(writeConfig(t, content)); err == nil {
			t.Errorf("%s: loaded, want an error", name)
		}
	}
}
