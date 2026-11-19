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

// The logins are under auth: auth login, auth context, or auth ctx; there
// is no top-level login or context command.
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
	for _, name := range []string{"context", "login"} {
		if c := newRoot().Command(name); c != nil {
			t.Errorf("a top-level %s command is still there", name)
		}
	}
	if newRoot().Command("auth").Command("login") == nil {
		t.Error("no auth login")
	}
}

// A resource named as kubectl names one, addressed in the context's
// namespace (auth context namespace) unless -n says otherwise, and a
// cluster-scoped one in none.
func TestVerbsResolveResourceAndNamespace(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/apis" {
			_, _ = w.Write([]byte(`[{"api":"system.infrapi.io","resources":[{"name":"namespaces"}]},
				{"api":"compute.infrapi.io","version":"v1","resources":[{"name":"vms","namespaced":true}]}]`))
			return
		}
		paths = append(paths, r.URL.Path)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	t.Setenv("INFRAPI_CONTEXT_FILE", filepath.Join(t.TempDir(), "config"))
	run := func(args ...string) error {
		return newRoot().Run(context.Background(), append([]string{"infrapi-cli"}, args...))
	}
	if err := run("auth", "ctx", "set", "dev", "--server", srv.URL, "--token", "t"); err != nil {
		t.Fatal(err)
	}
	if err := run("get", "vms"); err == nil || !strings.Contains(err.Error(), "is namespaced") {
		t.Fatalf("no namespace: %v", err)
	}
	if err := run("auth", "ctx", "ns", "thetenant"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"get", "vms"}, {"get", "vm.compute.infrapi.io", "-n", "core"}, {"get", "namespaces"}} {
		if err := run(args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	want := []string{"/apis/compute.infrapi.io/v1/namespaces/thetenant/vms", "/apis/compute.infrapi.io/v1/namespaces/core/vms", "/apis/system.infrapi.io/v1/namespaces"}
	if strings.Join(paths, " ") != strings.Join(want, " ") {
		t.Errorf("paths %v, want %v", paths, want)
	}
	// a login again keeps the namespace; --unset forgets it
	if err := run("auth", "ctx", "set", "dev", "--server", srv.URL, "--token", "t2"); err != nil {
		t.Fatal(err)
	}
	f, _ := clicontext.Load()
	if f.Contexts["dev"].Namespace != "thetenant" {
		t.Errorf("namespace after a new token: %q", f.Contexts["dev"].Namespace)
	}
	if err := run("auth", "ctx", "ns", "--unset"); err != nil {
		t.Fatal(err)
	}
	f, _ = clicontext.Load()
	if f.Contexts["dev"].Namespace != "" {
		t.Errorf("namespace after --unset: %q", f.Contexts["dev"].Namespace)
	}
}

// --server is a server's URL: "--server --server <url>" takes the second
// --server for the first's value, and the URL left is an argument too many.
func TestServerFlagIsAURL(t *testing.T) {
	t.Setenv("INFRAPI_CONTEXT_FILE", filepath.Join(t.TempDir(), "config"))
	for _, args := range [][]string{
		{"auth", "login", "--server", "--server", "http://localhost:8081"},
		{"auth", "login", "--server", "localhost:8081"},
		{"auth", "login", "--server", "http://localhost:8081", "extra"},
		{"auth", "ctx", "set", "dev", "--server", "--server", "http://localhost:8081", "--token", "t"},
		{"auth", "ctx", "set", "dev", "--server", "ftp://kayra.example.com", "--token", "t"},
	} {
		err := newRoot().Run(context.Background(), append([]string{"infrapi-cli"}, args...))
		if err == nil || !strings.Contains(err.Error(), "--server") && !strings.Contains(err.Error(), "argument") && !strings.Contains(err.Error(), "usage") {
			t.Errorf("%v: %v", args, err)
		}
	}
	if err := newRoot().Run(context.Background(), []string{"infrapi-cli", "auth", "ctx", "set", "dev", "--server", "http://localhost:8081/", "--token", "t"}); err != nil {
		t.Fatal(err)
	}
	if f, _ := clicontext.Load(); f.Contexts["dev"].Server != "http://localhost:8081" {
		t.Errorf("server: %q", f.Contexts["dev"].Server)
	}
}

// A login's access token is renewed before it expires, its new tokens
// saved; a login revoked asks for auth login; auth logout revokes it.
func TestContextRenewAndLogout(t *testing.T) {
	var bearers, revoked []string
	srv := httptest.NewUnstartedServer(nil)
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_, _ = w.Write([]byte(`{"issuer":"` + srv.URL + `","authorization_endpoint":"` + srv.URL + `/oauth2/authorize","token_endpoint":"` + srv.URL +
				`/oauth2/token","revocation_endpoint":"` + srv.URL + `/oauth2/revoke","code_challenge_methods_supported":["S256"]}`))
		case "/oauth2/token":
			_ = r.ParseForm()
			if r.PostForm.Get("refresh_token") != "refresh-1" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"its login is revoked"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"access-2","token_type":"Bearer","expires_in":900,"refresh_token":"refresh-2"}`))
		case "/oauth2/revoke":
			_ = r.ParseForm()
			revoked = append(revoked, r.PostForm.Get("token"))
		default:
			bearers = append(bearers, r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"sub":"jdoe"}`))
		}
	})
	srv.Start()
	defer srv.Close()
	t.Setenv("INFRAPI_CONTEXT_FILE", filepath.Join(t.TempDir(), "config"))
	f, _ := clicontext.Load()
	f.SetContext("dev", clicontext.Context{Server: srv.URL, Token: "access-1", RefreshToken: "refresh-1", Expiry: time.Now().Add(10 * time.Second)})
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) error {
		return newRoot().Run(context.Background(), append([]string{"infrapi-cli"}, args...))
	}
	if err := run("auth", "whoami"); err != nil {
		t.Fatal(err)
	}
	f, _ = clicontext.Load()
	if c := f.Contexts["dev"]; c.Token != "access-2" || c.RefreshToken != "refresh-2" || time.Until(c.Expiry) < 14*time.Minute {
		t.Fatalf("not renewed: %+v", c)
	}
	if len(bearers) != 1 || bearers[0] != "Bearer access-2" {
		t.Fatalf("bearers: %v", bearers)
	}
	// revoked meanwhile: asks for auth login
	c := f.Contexts["dev"]
	c.Expiry = time.Now()
	f.Contexts["dev"] = c
	_ = f.Save()
	if err := run("auth", "whoami"); err == nil || !strings.Contains(err.Error(), "auth login") {
		t.Fatalf("a revoked login: %v", err)
	}
	if err := run("auth", "logout"); err != nil {
		t.Fatal(err)
	}
	f, _ = clicontext.Load()
	if c := f.Contexts["dev"]; c.Token != "" || c.RefreshToken != "" || c.Server != srv.URL || len(revoked) != 1 || revoked[0] != "refresh-2" {
		t.Fatalf("logout: %+v %v", c, revoked)
	}
	if err := run("auth", "whoami"); err == nil || !strings.Contains(err.Error(), "logged out") {
		t.Fatalf("whoami, logged out: %v", err)
	}
}
