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
}
