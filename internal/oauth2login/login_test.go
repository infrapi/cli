package oauth2login

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeKayra is Kayra's authorization server, as the CLI sees it: its login
// succeeds at once.
type fakeKayra struct {
	srv       *httptest.Server
	mu        sync.Mutex
	codes     map[string]url.Values // code -> its authorization request
	refreshes map[string]bool       // refresh token -> still good
	revoked   []string
	iss       string // the iss its callback carries: its own when ""
}

func newFakeKayra(t *testing.T) *fakeKayra {
	t.Helper()
	k := &fakeKayra{codes: map[string]url.Values{}, refreshes: map[string]bool{}}
	k.srv = httptest.NewServer(http.HandlerFunc(k.serve))
	t.Cleanup(k.srv.Close)
	return k
}

func (k *fakeKayra) serve(w http.ResponseWriter, r *http.Request) {
	base := k.srv.URL
	switch r.URL.Path {
	case MetadataPath:
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": base, "authorization_endpoint": base + "/oauth2/authorize",
			"token_endpoint": base + "/oauth2/token", "revocation_endpoint": base + "/oauth2/revoke",
			"code_challenge_methods_supported": []string{"S256"}, "authorization_response_iss_parameter_supported": true})
	case "/oauth2/authorize":
		q := r.URL.Query()
		code := "code-" + q.Get("state")
		k.mu.Lock()
		k.codes[code] = q
		k.mu.Unlock()
		if q.Get("redirect_uri") == OOBRedirectURI {
			_, _ = io.WriteString(w, code)
			return
		}
		iss := k.iss
		if iss == "" {
			iss = base
		}
		http.Redirect(w, r, q.Get("redirect_uri")+"?"+url.Values{"code": {code}, "state": {q.Get("state")}, "iss": {iss}}.Encode(), http.StatusFound)
	case "/oauth2/token":
		_ = r.ParseForm()
		f := r.PostForm
		k.mu.Lock()
		defer k.mu.Unlock()
		switch f.Get("grant_type") {
		case "authorization_code":
			req, ok := k.codes[f.Get("code")]
			sum := sha256.Sum256([]byte(f.Get("code_verifier")))
			if !ok || req.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(sum[:]) || f.Get("client_id") != ClientID ||
				f.Get("redirect_uri") != req.Get("redirect_uri") || f.Get("resource") != base || req.Get("resource") != base {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"bad code"}`)
				return
			}
			delete(k.codes, f.Get("code"))
		case "refresh_token":
			if !k.refreshes[f.Get("refresh_token")] {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"its login is revoked"}`)
				return
			}
			k.refreshes[f.Get("refresh_token")] = false // rotated
		}
		rt := "refresh-" + random(8)
		k.refreshes[rt] = true
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-" + random(8), "token_type": "Bearer", "expires_in": 900, "refresh_token": rt})
	case "/oauth2/revoke":
		_ = r.ParseForm()
		k.mu.Lock()
		k.revoked = append(k.revoked, r.PostForm.Get("token"))
		k.refreshes[r.PostForm.Get("token")] = false
		k.mu.Unlock()
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// browse stands in for the browser: it follows Kayra's redirect to the
// loopback listener.
func browse(t *testing.T) {
	t.Helper()
	old := OpenBrowser
	OpenBrowser = func(u string) error {
		go func() {
			resp, err := http.Get(u)
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}
	t.Cleanup(func() { OpenBrowser = old })
}

func TestLoginRefreshRevoke(t *testing.T) {
	k := newFakeKayra(t)
	browse(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := LoginToServer(ctx, k.srv.URL, io.Discard)
	if err != nil || !strings.HasPrefix(r.AccessToken, "access-") || r.RefreshToken == "" || time.Until(r.Expiry) < 14*time.Minute {
		t.Fatalf("login: %+v %v", r, err)
	}
	r2, err := Refresh(ctx, k.srv.URL, r.RefreshToken)
	if err != nil || r2.RefreshToken == r.RefreshToken {
		t.Fatalf("refresh: %+v %v", r2, err)
	}
	_, err = Refresh(ctx, k.srv.URL, r.RefreshToken)
	var oerr *Error
	if !errors.As(err, &oerr) || oerr.Code != "invalid_grant" {
		t.Fatalf("a used refresh token: %v", err)
	}
	if err := Revoke(ctx, k.srv.URL, r2.RefreshToken); err != nil || len(k.revoked) != 1 {
		t.Fatalf("revoke: %v %v", err, k.revoked)
	}
}

// The callback's issuer must be the server's (RFC 9207): a mix-up ends the
// login, its code unused.
func TestLoginRefusesAnotherIssuer(t *testing.T) {
	k := newFakeKayra(t)
	k.iss = "https://evil.example.com"
	browse(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := LoginToServer(ctx, k.srv.URL, io.Discard); err == nil || !strings.Contains(err.Error(), "issuer") {
		t.Fatalf("another issuer: %v", err)
	}
}

// --no-browser: the code Kayra shows, pasted.
func TestLoginManual(t *testing.T) {
	k := newFakeKayra(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pr, pw := io.Pipe()
	stderr := &lockedBuilder{}
	done := make(chan struct{})
	var r *Result
	var err error
	go func() { r, err = LoginToServerManual(ctx, k.srv.URL, pr, stderr); close(done) }()
	// what the human does: open the printed URL, copy the code shown
	var authURL string
	for authURL == "" {
		time.Sleep(10 * time.Millisecond)
		for _, f := range strings.Fields(stderr.String()) {
			if strings.HasPrefix(f, k.srv.URL+"/oauth2/authorize") {
				authURL = f
			}
		}
	}
	resp, gerr := http.Get(authURL)
	if gerr != nil {
		t.Fatal(gerr)
	}
	code, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	_, _ = io.WriteString(pw, "  "+string(code)+"\n")
	<-done
	if err != nil || r.RefreshToken == "" {
		t.Fatalf("manual: %+v %v", r, err)
	}
}

func TestDiscoverRefusesNoS256(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": "x", "authorization_endpoint": "x", "token_endpoint": "x", "code_challenge_methods_supported": []string{"plain"}})
	}))
	t.Cleanup(srv.Close)
	if _, err := discover(context.Background(), srv.URL); err == nil || !strings.Contains(err.Error(), "S256") {
		t.Fatalf("no S256: %v", err)
	}
}

// lockedBuilder is a strings.Builder written and read from two goroutines.
type lockedBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuilder) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuilder) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
