package dev

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The dev.yaml written from the answers loads as it is, every setting
// commented.
func TestWriteConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), ConfigName)
	if err := WriteConfig(path, Answers{Domain: "example.org", Platform: "preprod", API: "compute.example.com", Port: 8088}); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Domain != "example.org" || c.Platform != "preprod" || c.Region != "eu-west-1" || c.Kayra.Image != "ghcr.io/infrapi/kayra:latest" ||
		c.App.API != "compute.example.com" || c.App.Port != 8088 || c.SysadminGroups[0] != "preprod-core-sysadmin" ||
		c.Users[1].Groups[0] != "preprod-acme-admins" {
		t.Errorf("dev.yaml read back: %+v", c)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{"  # config:\n", "#   reconciler:\n", "# gateway:\n", "# compose:\n", "  # env:\n"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("dev.yaml has no %q", want)
		}
	}

	// never over one
	if err := WriteConfig(path, Answers{}); !errors.Is(err, ErrConfigExists) {
		t.Errorf("over an existing dev.yaml: %v", err)
	}
}

// Answers dev.yaml would refuse write nothing.
func TestWriteConfig_refuses(t *testing.T) {
	for name, a := range map[string]Answers{
		"domain":   {Domain: "Example"},
		"platform": {Platform: "staging"},
		"api":      {API: "Compute"},
	} {
		path := filepath.Join(t.TempDir(), ConfigName)
		if err := WriteConfig(path, a); err == nil {
			t.Errorf("%s: no error", name)
		}
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s: dev.yaml written", name)
		}
	}
}
