package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/infrapi/cli/internal/clicontext"
)

// auth sessions lists and revokes at the server's /oauth2/sessions, with
// the context's token; revoke takes an id or --all, not both.
func TestSessionsCommands(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Query().Get("user") == "asmith":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"detail":"your cluster roles do not grant \"list\" on sessions: only your own"}`))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[{"id":"s1","user_id":"jdoe","client_id":"infrapi-cli","created_at":"2026-11-19T10:00:00Z","last_used_at":"2026-11-19T11:00:00Z","expires_at":"2026-12-19T10:00:00Z"}]`))
		case r.URL.Path == "/oauth2/sessions":
			_, _ = w.Write([]byte(`{"revoked":2}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	t.Setenv("INFRAPI_CONTEXT_FILE", filepath.Join(t.TempDir(), "config"))
	f, _ := clicontext.Load()
	f.SetContext("dev", clicontext.Context{Server: srv.URL, Token: "tok", Expiry: time.Now().Add(time.Hour)})
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) error {
		return newRoot().Run(context.Background(), append([]string{"infrapi-cli", "auth", "sessions"}, args...))
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	if err := run("--user", "asmith"); err == nil || !strings.Contains(err.Error(), "only your own") {
		t.Fatalf("another's: %v", err)
	}
	if err := run("revoke", "s1"); err != nil {
		t.Fatal(err)
	}
	if err := run("revoke", "--all", "--user", "asmith"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]string{{"revoke"}, {"revoke", "s1", "--all"}} {
		if err := run(bad...); err == nil {
			t.Errorf("%v: accepted", bad)
		}
	}
	want := []string{
		"GET /oauth2/sessions Bearer tok",
		"GET /oauth2/sessions?user=asmith Bearer tok",
		"DELETE /oauth2/sessions/s1 Bearer tok",
		"DELETE /oauth2/sessions?user=asmith Bearer tok",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s", strings.Join(calls, "\n"))
	}
}
