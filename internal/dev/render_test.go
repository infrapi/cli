package dev

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
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
	c, err := LoadConfig(writeConfig(t, "domain: example.org\nports: {gateway: 19080}\n"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := Render(c, dir, RenderOptions{Force: true}); err != nil {
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
		"GATEWAY_PORT=19080\n", "KAYRA_IMAGE=ghcr.io/infrapi/kayra:latest\n", "KAYRA_PULL_POLICY=missing\n"} {
		if !strings.Contains(env, want) {
			t.Errorf("env misses %q:\n%s", want, env)
		}
	}
	example := read(t, filepath.Join(dir, "app.env"))
	for _, want := range []string{`INFRAPI_APP_NAME="compute"`, `COMPUTE_AUTH_URL="http://localhost:19080"`} {
		if !strings.Contains(example, want) {
			t.Errorf("app.env misses %q", want)
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
	if _, err := Render(c, dir, RenderOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	first := key()
	if _, err := Render(c, dir, RenderOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if key() != first {
		t.Fatal("a render without newKeys changed the keys")
	}
	if _, err := Render(c, dir, RenderOptions{NewKeys: true, Force: true}); err != nil {
		t.Fatal(err)
	}
	if key() == first {
		t.Fatal("newKeys kept the keys")
	}
	if st, _ := os.Stat(filepath.Join(dir, "keys", "self.pem")); st.Mode().Perm() != 0o600 {
		t.Errorf("key mode %v, want 0600", st.Mode().Perm())
	}
}

// The API's identity key: EC P-256, 0600, kept across renders, named by
// app.env by its absolute path.
func TestRender_identityKey(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "keys", IdentityKey)
	r, err := Render(c, dir, RenderOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Diff, "keys/app-identity.pem: new identity key of the API sha256:") {
		t.Errorf("diff: %s", r.Diff)
	}
	first := read(t, path)
	b, _ := pem.Decode([]byte(first))
	key, err := x509.ParsePKCS8PrivateKey(b.Bytes)
	if ec, ok := key.(*ecdsa.PrivateKey); err != nil || !ok || ec.Curve != elliptic.P256() {
		t.Fatalf("not an EC P-256 key: %T %v", key, err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	if env := read(t, filepath.Join(dir, "app.env")); !strings.Contains(env, `INFRAPI_IDENTITY_KEY_PATH="`+path+`"`) {
		t.Errorf("app.env: %s", env)
	}
	if r, err := Render(c, dir, RenderOptions{}); err != nil || r.Pending || read(t, path) != first {
		t.Fatalf("a second render: %v %+v", err, r)
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
	if _, err := Render(c, dir, RenderOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "kayra.yaml")
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	if _, err := Render(c, dir, RenderOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); !st.ModTime().Equal(past) {
		t.Error("an unchanged kayra.yaml was written again")
	}
	c.Domain = "example.org"
	if _, err := Render(c, dir, RenderOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.ModTime().Equal(past) {
		t.Error("a changed kayra.yaml was not written")
	}
}

// Rendered.Kayra tells whether what Kayra and gateway-init read changed: a
// new port in env only does not.
func TestRender_reportsKayraChanges(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	r, err := Render(c, dir, RenderOptions{Force: true})
	if err != nil || !r.Kayra {
		t.Fatalf("first render: %+v %v, want Kayra changed", r, err)
	}
	if r, err = Render(c, dir, RenderOptions{Force: true}); err != nil || r.Kayra {
		t.Fatalf("same render: %+v %v, want nothing changed", r, err)
	}
	c.App.Port = 18080 // app.env only
	if r, err = Render(c, dir, RenderOptions{Force: true}); err != nil || r.Kayra {
		t.Fatalf("example port: %+v %v, want Kayra unchanged", r, err)
	}
	c.Domain = "example.org"
	if r, err = Render(c, dir, RenderOptions{Force: true}); err != nil || !r.Kayra {
		t.Fatalf("domain: %+v %v, want Kayra changed", r, err)
	}
}

// Without Force, a render that would change a file writes nothing: its
// diff shows what it would do, a file edited by hand in the render's dir
// included.
func TestRender_cancelsWithoutForce(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), ".infrapi-dev")

	r, err := Render(c, dir, RenderOptions{})
	if err != nil || !r.Pending {
		t.Fatalf("first render: %+v %v, want pending", r, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("a canceled render created %s: %v", dir, err)
	}
	for _, want := range []string{"keys/self.pem: new signing key sha256:", "kayra.yaml: new file\n"} {
		if !strings.Contains(r.Diff, want) {
			t.Errorf("first render's diff misses %q:\n%s", want, r.Diff)
		}
	}

	if r, err = Render(c, dir, RenderOptions{Force: true}); err != nil || r.Pending || len(r.Files) == 0 {
		t.Fatalf("forced render: %+v %v", r, err)
	}
	if r, err = Render(c, dir, RenderOptions{}); err != nil || r.Pending || r.Diff != "" {
		t.Fatalf("render of a rendered stack: %+v %v, want nothing to do", r, err)
	}

	// edited by hand
	path := filepath.Join(dir, "kayra.yaml")
	edited := strings.Replace(read(t, path), "max_concurrent_reconciles: 5", "max_concurrent_reconciles: 50", 1)
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	// and a change in dev.yaml: canceled too
	c.App.Port = 18080
	if r, err = Render(c, dir, RenderOptions{}); err != nil || !r.Pending {
		t.Fatalf("render: %+v %v, want pending", r, err)
	}
	for _, want := range []string{"--- a/kayra.yaml", "-  max_concurrent_reconciles: 50", "+  max_concurrent_reconciles: 5", "--- a/app.env", "+INFRAPI_APP_LISTEN_ADDRESS=\"0.0.0.0:18080\""} {
		if !strings.Contains(r.Diff, want) {
			t.Errorf("diff misses %q:\n%s", want, r.Diff)
		}
	}
	if got := strings.Join(r.Files, " "); got != "app.env kayra.yaml" {
		t.Errorf("files = %q", got)
	}
	if read(t, path) != edited {
		t.Error("a canceled render overwrote kayra.yaml")
	}
	if r, err = Render(c, dir, RenderOptions{Force: true}); err != nil || r.Pending || read(t, path) == edited {
		t.Fatalf("forced render: %+v %v, kayra.yaml left as edited", r, err)
	}
}

// The diff never prints a signing key: new keys show as fingerprints.
func TestRender_diffHidesKeys(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := Render(c, dir, RenderOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	r, err := Render(c, dir, RenderOptions{NewKeys: true, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Split(read(t, filepath.Join(dir, "keys", "self.pem")), "\n")[1]
	if strings.Contains(r.Diff, key) {
		t.Fatalf("the diff prints a key:\n%s", r.Diff)
	}
	for _, want := range []string{"keys/local.pem: new signing key", "-          -----PRIVATE KEY sha256:", "+          -----PRIVATE KEY sha256:"} {
		if !strings.Contains(r.Diff, want) {
			t.Errorf("the diff misses %q:\n%s", want, r.Diff)
		}
	}
}
