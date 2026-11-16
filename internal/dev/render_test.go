package dev

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Another domain reaches every file: Kayra's api_domain, its healthcheck,
// the gateway's route to Kayra's groups, the example's group.
func TestRender_domain(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, "domain: example.org\nports: {gateway: 19080}\napis: [{name: compute}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := Render(c, dir, false); err != nil {
		t.Fatal(err)
	}

	kayra := read(t, filepath.Join(dir, "kayra.yaml"))
	for _, want := range []string{`api_domain: "example.org"`, "- sandbox-core-sysadmin", "-----BEGIN PRIVATE KEY-----",
		"{{ .Subscription.Api }}"} {
		if !strings.Contains(kayra, want) {
			t.Errorf("kayra.yaml misses %q", want)
		}
	}
	env := read(t, filepath.Join(dir, "env"))
	for _, want := range []string{"DEV_DOMAIN=example.org\n", "KAYRA_HEALTHZ=/apis/system.example.org/v1/-/healthz\n",
		"GATEWAY_PORT=19080\n", "KAYRA_IMAGE=ghcr.io/infrapi/kayra:latest\n", "KAYRA_PULL_POLICY=missing\n",
		"EXAMPLE_API=compute.example.org\n"} {
		if !strings.Contains(env, want) {
			t.Errorf("env misses %q:\n%s", want, env)
		}
	}
	example := read(t, filepath.Join(dir, "example.env"))
	for _, want := range []string{`COMPUTE_API_GROUP="compute.example.org"`, `COMPUTE_AUTH_URL="http://localhost:19080"`} {
		if !strings.Contains(example, want) {
			t.Errorf("example.env misses %q", want)
		}
	}

	var r struct {
		URIs []string `json:"uris"`
	}
	if err := json.Unmarshal([]byte(read(t, filepath.Join(dir, "routes", "00-kayra.json"))), &r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(r.URIs, " "), "/apis/catalog.example.org/*") {
		t.Errorf("kayra route: %v", r.URIs)
	}
	stub := read(t, filepath.Join(dir, "routes", "01-openapi-stub.json"))
	if !strings.Contains(stub, "serverless-pre-function") || !strings.Contains(stub, "widgets") {
		t.Errorf("openapi-stub route: %s", stub)
	}
}

// The keys are generated once and kept; newKeys generates others.
func TestRender_keys(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	key := func() string { return read(t, filepath.Join(dir, "keys", "self.pem")) }
	if err := Render(c, dir, false); err != nil {
		t.Fatal(err)
	}
	first := key()
	if err := Render(c, dir, false); err != nil {
		t.Fatal(err)
	}
	if key() != first {
		t.Fatal("a render without newKeys changed the keys")
	}
	if err := Render(c, dir, true); err != nil {
		t.Fatal(err)
	}
	if key() == first {
		t.Fatal("newKeys kept the keys")
	}
	if st, _ := os.Stat(filepath.Join(dir, "keys", "self.pem")); st.Mode().Perm() != 0o600 {
		t.Errorf("key mode %v, want 0600", st.Mode().Perm())
	}
}

// kayra.src builds kayra-dev:local, which is never pulled.
func TestRender_src(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, "kayra: {src: ../kayra}\n"))
	if err != nil {
		t.Fatal(err)
	}
	env := envFile(c)
	if !strings.Contains(env, "KAYRA_IMAGE=kayra-dev:local\n") || !strings.Contains(env, "KAYRA_PULL_POLICY=never\n") ||
		!strings.Contains(env, "KAYRA_SRC=../kayra\n") {
		t.Errorf("env: %s", env)
	}
}

// A render that changes nothing leaves the files as they were: make up
// reloads Kayra only when one changed.
func TestRender_unchanged(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := Render(c, dir, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "kayra.yaml")
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	if err := Render(c, dir, false); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); !st.ModTime().Equal(past) {
		t.Error("an unchanged kayra.yaml was written again")
	}
	c.Domain = "example.org"
	if err := Render(c, dir, false); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.ModTime().Equal(past) {
		t.Error("a changed kayra.yaml was not written")
	}
}
