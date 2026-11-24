package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/infrapi/cli/internal/clicontext"
)

// app init, add and generate write a project; app register posts its
// registration to the current context's Kayra, its catalog from discovery.
func TestApp(t *testing.T) {
	t.Setenv("PATH", "") // no go: no go mod tidy
	dir := t.TempDir()
	t.Chdir(dir)
	run := func(args ...string) error {
		return newRoot().Run(context.Background(), append([]string{"infrapi-cli"}, args...))
	}
	if err := run("app", "init", "--no-prompt", "--module", "github.com/acme/compute", "--ci", "none", "compute"); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(dir, "compute", "internal"))
	if err := run("app", "add", "vm"); err != nil {
		t.Fatal(err)
	}
	if err := run("app", "add", "vm"); err == nil {
		t.Error("vm added twice")
	}
	if err := run("app", "generate"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "compute", "internal", "compute", "vms.go")); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/.well-known/kayra.json" {
			_, _ = w.Write([]byte(`{"domain": "infrapi.io", "apis": {"catalog": "/apis/catalog.infrapi.io/v1"}, "endpoints": {"check": "/apis/authorization.infrapi.io/v1/check"}}`))
			return
		}
		path = r.Method + " " + r.URL.Path
		b, _ := io.ReadAll(r.Body)
		got = nil
		_ = json.Unmarshal(b, &got)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	t.Setenv("INFRAPI_CONTEXT_FILE", filepath.Join(dir, "contexts"))
	f, err := clicontext.Load()
	if err != nil {
		t.Fatal(err)
	}
	f.SetContext("dev-admin", clicontext.Context{Server: srv.URL, Token: "t", TokenType: "Bearer"})
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	if err := run("app", "register", "--no-route", "--owner", "sre"); err != nil {
		t.Fatal(err)
	}
	if path != "POST /apis/catalog.infrapi.io/v1/apiregistrations" || got["name"] != "compute.infrapi.io" || got["short_name"] != "compute" ||
		got["version"] != "v1" || got["owner"] != "sre" || got["active"] != true {
		t.Errorf("%s %v", path, got)
	}
	if id, _ := got["identity"].(map[string]any); id["jwks_from"] != "spec" {
		t.Errorf("identity: %v", got["identity"])
	}

	// app keygen: the key, its public half pinned by --jwks
	key := filepath.Join(dir, "identity.pem")
	if err := run("app", "keygen", key); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(key); st == nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key: %v", st)
	}
	if err := run("app", "keygen", key); err == nil {
		t.Error("keygen replaced a key without --force")
	}
	if err := run("app", "register", "--no-route", "--jwks", key); err != nil {
		t.Fatal(err)
	}
	id, _ := got["identity"].(map[string]any)
	jwks, _ := id["jwks"].(map[string]any)
	keys, _ := jwks["keys"].([]any)
	if k, _ := firstOf(keys).(map[string]any); len(keys) != 1 || k["kty"] != "EC" || k["kid"] == "" || k["d"] != nil {
		t.Errorf("--jwks: %v", got["identity"])
	}
	if err := run("app", "register", "--no-route", "--no-identity"); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["identity"]; ok {
		t.Errorf("--no-identity: %v", got["identity"])
	}
}

func firstOf(l []any) any {
	if len(l) == 0 {
		return nil
	}
	return l[0]
}

// app run and app register work with the stack DEV_CONFIG (or --config)
// names, as the dev commands do; the project's dev.yaml otherwise; none
// without either.
func TestProjectStack(t *testing.T) {
	root := t.TempDir()
	stackOf := func(env string, args ...string) (string, error) {
		t.Setenv("DEV_CONFIG", env)
		var got string
		cmd := &cli.Command{Name: "run", Flags: []cli.Flag{devConfigFlag()}, Action: func(_ context.Context, cmd *cli.Command) error {
			s, err := projectStack(cmd, root)
			if s != nil {
				got = s.ConfigPath
			}
			return err
		}}
		err := cmd.Run(context.Background(), append([]string{"run"}, args...))
		return got, err
	}
	if got, err := stackOf(""); err != nil || got != "" {
		t.Fatalf("no dev.yaml: %q, %v", got, err)
	}
	cfg := "domain: infrapi.io\nplatform: sandbox\nregion: eu-west-1\nlocation: dc1\n"
	for _, name := range []string{"dev.yaml", "dev.local.yaml"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := stackOf(""); err != nil || got != filepath.Join(root, "dev.yaml") {
		t.Fatalf("the project's: %q, %v", got, err)
	}
	local := filepath.Join(root, "dev.local.yaml")
	if got, err := stackOf(local); err != nil || got != local {
		t.Fatalf("DEV_CONFIG: %q, %v", got, err)
	}
	if got, err := stackOf("", "--config", local); err != nil || got != local {
		t.Fatalf("--config: %q, %v", got, err)
	}
	if _, err := stackOf(filepath.Join(root, "missing.yaml")); err == nil {
		t.Fatal("DEV_CONFIG naming no file: no error")
	}
}
