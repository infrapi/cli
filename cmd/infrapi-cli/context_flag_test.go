package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/infrapi/cli/internal/clicontext"
)

// --context, before or after the command, or $INFRAPI_CONTEXT, picks a saved
// context for one call: the current one stays as it is.
func TestContextFlagOverridesForOneCall(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sub":"someone"}`))
	}))
	defer srv.Close()

	file := filepath.Join(t.TempDir(), "config")
	t.Setenv("INFRAPI_CONTEXT_FILE", file)
	f, err := clicontext.Load()
	if err != nil {
		t.Fatal(err)
	}
	f.SetContext("other", clicontext.Context{Server: srv.URL, Token: "token-other", TokenType: "Bearer"})
	f.SetContext("current", clicontext.Context{Server: srv.URL, Token: "token-current", TokenType: "Bearer"})
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) {
		t.Helper()
		if err := newRoot().Run(context.Background(), append([]string{"infrapi-cli"}, args...)); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	run("auth", "whoami")
	run("--context", "other", "auth", "whoami")
	run("auth", "whoami", "--context", "other")
	t.Setenv("INFRAPI_CONTEXT", "other")
	run("auth", "whoami")

	want := []string{"Bearer token-current", "Bearer token-other", "Bearer token-other", "Bearer token-other"}
	if len(got) != len(want) {
		t.Fatalf("calls %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d: %s, want %s", i, got[i], want[i])
		}
	}

	f, err = clicontext.Load()
	if err != nil {
		t.Fatal(err)
	}
	if f.CurrentContext != "current" {
		t.Errorf("current context is %q, want it unchanged", f.CurrentContext)
	}

	t.Setenv("INFRAPI_CONTEXT", "")
	if err := newRoot().Run(context.Background(), []string{"infrapi-cli", "--context", "nope", "auth", "whoami"}); err == nil {
		t.Error("unknown context: no error")
	}
}

// The saved logins are under auth: auth context, or auth ctx; there is no
// top-level context command.
func TestAuthContext(t *testing.T) {
	t.Setenv("INFRAPI_CONTEXT_FILE", filepath.Join(t.TempDir(), "config"))
	run := func(args ...string) error {
		return newRoot().Run(context.Background(), append([]string{"infrapi-cli"}, args...))
	}
	if err := run("auth", "context", "set", "a", "--server", "https://kayra.example.com", "--token", "t"); err != nil {
		t.Fatal(err)
	}
	if err := run("auth", "ctx", "set", "b", "--server", "https://kayra.example.com", "--token", "t"); err != nil {
		t.Fatal(err)
	}
	if err := run("auth", "ctx", "use", "a"); err != nil {
		t.Fatal(err)
	}
	f, err := clicontext.Load()
	if err != nil {
		t.Fatal(err)
	}
	if f.CurrentContext != "a" || len(f.Contexts) != 2 {
		t.Errorf("current %q, contexts %v", f.CurrentContext, f.Names())
	}
	if c := newRoot().Command("context"); c != nil {
		t.Error("a top-level context command is still there")
	}
}
