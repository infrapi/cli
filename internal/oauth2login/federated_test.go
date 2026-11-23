package oauth2login

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceAccount(t *testing.T) {
	if id, err := ServiceAccount("core/deployer"); err != nil || id != "system:serviceaccount:core:deployer" {
		t.Fatalf("%q %v", id, err)
	}
	for _, bad := range []string{"deployer", "core/", "/deployer", "a/b/c"} {
		if _, err := ServiceAccount(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestIDToken(t *testing.T) {
	ctx := context.Background()
	t.Setenv("KAYRA_ID_TOKEN", " job-token\n")
	if v, err := IDToken(ctx, "env:KAYRA_ID_TOKEN", "aud"); err != nil || v != "job-token" {
		t.Fatalf("env: %q %v", v, err)
	}
	if _, err := IDToken(ctx, "env:NOPE", "aud"); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("an unset variable: %v", err)
	}
	path := filepath.Join(t.TempDir(), "token")
	_ = os.WriteFile(path, []byte("file-token\n"), 0o600)
	if v, err := IDToken(ctx, "file:"+path, "aud"); err != nil || v != "file-token" {
		t.Fatalf("file: %q %v", v, err)
	}
	if _, err := IDToken(ctx, "file:/nope", "aud"); err == nil {
		t.Fatal("a missing file")
	}
	if _, err := IDToken(ctx, "vault:x", "aud"); err == nil || !strings.Contains(err.Error(), "env:<NAME>") {
		t.Fatalf("an unknown source: %v", err)
	}

	// GitHub Actions: its request URL, its bearer, the audience asked
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gh-request" || r.URL.Query().Get("audience") != "https://kayra.example.com" || r.URL.Query().Get("api-version") != "2.0" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"value": "gh-token"})
	}))
	defer gh.Close()
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", gh.URL+"/token?api-version=2.0")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "gh-request")
	if v, err := IDToken(ctx, "github", "https://kayra.example.com"); err != nil || v != "gh-token" {
		t.Fatalf("github: %q %v", v, err)
	}
	if _, err := IDToken(ctx, "github", "https://elsewhere.example.com"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("github refusing: %v", err)
	}
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	if _, err := IDToken(ctx, "github", "aud"); err == nil || !strings.Contains(err.Error(), "id-token: write") {
		t.Fatalf("not in GitHub Actions: %v", err)
	}
}

// TestFederatedLogin: the client_credentials request, the job's token its
// assertion, for Kayra's issuer.
func TestFederatedLogin(t *testing.T) {
	k := newFakeKayra(t)
	var form map[string][]string
	k.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/token" {
			_ = r.ParseForm()
			form = r.PostForm
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "sa-token", "token_type": "Bearer", "expires_in": 900})
			return
		}
		k.serve(w, r)
	})
	t.Setenv("JOB", "job-token")
	r, err := FederatedLogin(context.Background(), k.srv.URL, "system:serviceaccount:core:deployer", "env:JOB")
	if err != nil || r.AccessToken != "sa-token" || r.RefreshToken != "" || r.Expiry.IsZero() {
		t.Fatalf("%+v %v", r, err)
	}
	want := map[string]string{"grant_type": "client_credentials", "client_id": "system:serviceaccount:core:deployer",
		"client_assertion_type": JWTBearer, "client_assertion": "job-token", "resource": k.srv.URL}
	for key, v := range want {
		if got := form[key]; len(got) != 1 || got[0] != v {
			t.Errorf("%s = %v, want %q", key, got, v)
		}
	}
	if _, err := FederatedLogin(context.Background(), k.srv.URL, "x", "env:NOPE"); err == nil {
		t.Fatal("no token: no login")
	}
}
