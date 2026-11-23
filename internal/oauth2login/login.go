// Package oauth2login is infrapi-cli's OAuth 2.1 client of Kayra's
// authorization server: `infrapi-cli auth login` logs in (authorization code,
// PKCE S256), each command renews the token before it expires (Refresh), and
// `infrapi-cli auth logout` revokes it (Revoke).
//
// It knows nothing but a server URL. Kayra's metadata
// (/.well-known/oauth-authorization-server) names its endpoints; the human
// logs in at the provider Kayra sends them to (Okta, say), never at the CLI;
// the tokens are Kayra's own: an access token, short-lived, its aud the
// server, and a refresh token, rotated at each use. The CLI is Kayra's
// built-in client, infrapi-cli: a loopback redirect, any port (RFC 8252), or
// on a host with no browser a code to paste (LoginToServerManual).
package oauth2login

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/infrapi/cli/internal/ui"
)

// ClientID is infrapi-cli's client at Kayra, built in.
const ClientID = "infrapi-cli"

// OOBRedirectURI is LoginToServerManual's redirect URI: Kayra shows the code
// to paste instead of redirecting.
const OOBRedirectURI = "urn:ietf:wg:oauth:2.0:oob"

// MetadataPath is Kayra's authorization server metadata (RFC 8414).
const MetadataPath = "/.well-known/oauth-authorization-server"

// callbackPath is where the loopback listener catches Kayra's redirect.
const callbackPath = "/callback"

// Result is a login's, or a renewal's, tokens.
type Result struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	Expiry       time.Time
}

// OpenBrowser is swappable for tests: in production it best-effort
// launches the default browser; the URL is printed either way.
var OpenBrowser = defaultOpenBrowser

// httpClient is every call to Kayra of the login: the proxy is the
// environment's (netproxy).
var httpClient = &http.Client{Timeout: 30 * time.Second}

// metadata is Kayra's authorization server metadata, what the CLI uses of
// it.
type metadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RevocationEndpoint    string   `json:"revocation_endpoint"`
	CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
	IssParameter          bool     `json:"authorization_response_iss_parameter_supported"`
}

// discover reads server's metadata.
func discover(ctx context.Context, server string) (metadata, error) {
	var m metadata
	base := strings.TrimRight(server, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+MetadataPath, nil)
	if err != nil {
		return m, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return m, fmt.Errorf("oauth2login: %s: %w", base+MetadataPath, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return m, fmt.Errorf("oauth2login: %s answered %d: is %s a Kayra with its authorization server on (oauth2.enabled)?", base+MetadataPath, resp.StatusCode, base)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return m, fmt.Errorf("oauth2login: %s: %w", base+MetadataPath, err)
	}
	if m.AuthorizationEndpoint == "" || m.TokenEndpoint == "" {
		return m, fmt.Errorf("oauth2login: %s names no authorization or token endpoint", base+MetadataPath)
	}
	if !slices.Contains(m.CodeChallengeMethods, "S256") {
		return m, fmt.Errorf("oauth2login: %s does not support PKCE S256", base)
	}
	return m, nil
}

// login is one login attempt: its state, PKCE verifier and the URL to open.
type login struct {
	meta     metadata
	server   string
	redirect string
	state    string
	verifier string
	url      string
}

func start(ctx context.Context, server, redirect string) (login, error) {
	m, err := discover(ctx, server)
	if err != nil {
		return login{}, err
	}
	l := login{meta: m, server: strings.TrimRight(server, "/"), redirect: redirect, state: random(24), verifier: oauth2.GenerateVerifier()}
	l.url = m.AuthorizationEndpoint + "?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {ClientID},
		"redirect_uri":          {redirect},
		"state":                 {l.state},
		"code_challenge":        {oauth2.S256ChallengeFromVerifier(l.verifier)},
		"code_challenge_method": {"S256"},
		"resource":              {l.server},
	}.Encode()
	return l, nil
}

// LoginToServer logs in through the browser: a loopback listener catches
// Kayra's redirect, its state and issuer checked, then the code is redeemed
// with this process's own verifier. ctx bounds the wait; status lines,
// the URL to open included, go to stderr.
func LoginToServer(ctx context.Context, server string, stderr io.Writer) (*Result, error) {
	if server == "" {
		return nil, fmt.Errorf("oauth2login: server is required")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("oauth2login: open the local callback listener: %w", err)
	}
	l, err := start(ctx, server, fmt.Sprintf("http://%s%s", ln.Addr(), callbackPath))
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	codeCh, errCh := make(chan string, 1), make(chan error, 1)
	srv := &http.Server{Handler: l.callback(codeCh, errCh), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	_, _ = fmt.Fprintf(ui.Writer(stderr), "oauth2login: opening the browser to log in:\n\n  %s\n\n", ui.URL(l.url))
	if err := OpenBrowser(l.url); err != nil {
		_, _ = fmt.Fprintf(ui.Writer(stderr), "oauth2login: %s (%v): open the URL above by hand\n", ui.Warn("could not open a browser"), err)
	}
	select {
	case code := <-codeCh:
		return l.redeem(ctx, code)
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, fmt.Errorf("oauth2login: timed out waiting for the browser login: %w", ctx.Err())
	}
}

// callback answers Kayra's redirect: its state this login's, its issuer
// the server's (RFC 9207), then its code; an error, or a mismatch, ends
// the login.
func (l login) callback(codeCh chan<- string, errCh chan<- error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		fail := func(msg string) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, "Login failed: %s\nCheck the terminal, and run infrapi-cli auth login again.\n", msg) //#nosec G705 -- text/plain, never rendered as HTML
			errCh <- fmt.Errorf("oauth2login: %s", msg)
		}
		switch {
		case q.Get("state") != l.state:
			fail("the callback's state is not this login's")
		case (l.meta.IssParameter || q.Get("iss") != "") && q.Get("iss") != l.meta.Issuer:
			// a mismatch: nothing else in the response is trusted, its error included
			fail(fmt.Sprintf("the callback's issuer %q is not %q", q.Get("iss"), l.meta.Issuer))
		case q.Get("error") != "":
			fail(q.Get("error") + ": " + q.Get("error_description"))
		case q.Get("code") == "":
			fail("the callback carries no code")
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, "<html><body><p>Logged in: you can close this tab.</p></body></html>")
			codeCh <- q.Get("code")
		}
	})
	return mux
}

// LoginToServerManual logs in on a host with no browser (SSH, a container):
// it prints the URL to open on any machine, where Kayra shows a code once
// the human has logged in, and reads that code from stdin. The code redeems
// with this process's verifier alone.
func LoginToServerManual(ctx context.Context, server string, stdin io.Reader, stderr io.Writer) (*Result, error) {
	if server == "" {
		return nil, fmt.Errorf("oauth2login: server is required")
	}
	l, err := start(ctx, server, OOBRedirectURI)
	if err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(ui.Writer(stderr), "oauth2login: open this URL in a browser on any machine to log in:\n\n  %s\n\n", ui.URL(l.url))
	_, _ = fmt.Fprint(ui.Writer(stderr), ui.Bold("Paste the code shown in the browser: "))
	lineCh, errCh := make(chan string, 1), make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && strings.TrimSpace(line) == "" {
			errCh <- fmt.Errorf("oauth2login: read the code: %w", err)
			return
		}
		lineCh <- strings.TrimSpace(line)
	}()
	select {
	case code := <-lineCh:
		if code == "" {
			return nil, fmt.Errorf("oauth2login: no code pasted")
		}
		return l.redeem(ctx, code)
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, fmt.Errorf("oauth2login: timed out waiting for the code: %w", ctx.Err())
	}
}

func (l login) redeem(ctx context.Context, code string) (*Result, error) {
	return post(ctx, l.meta.TokenEndpoint, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {l.redirect},
		"client_id":     {ClientID},
		"code_verifier": {l.verifier},
		"resource":      {l.server},
	})
}

// Refresh renews a login's tokens: a new access token, and a new refresh
// token, the one given used up.
func Refresh(ctx context.Context, server, refreshToken string) (*Result, error) {
	m, err := discover(ctx, server)
	if err != nil {
		return nil, err
	}
	return post(ctx, m.TokenEndpoint, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {ClientID},
		"resource":      {strings.TrimRight(server, "/")},
	})
}

// Revoke revokes a token at Kayra: a refresh token, its whole login.
func Revoke(ctx context.Context, server, token string) error {
	m, err := discover(ctx, server)
	if err != nil {
		return err
	}
	if m.RevocationEndpoint == "" {
		return fmt.Errorf("oauth2login: %s names no revocation endpoint", server)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.RevocationEndpoint,
		strings.NewReader(url.Values{"token": {token}, "client_id": {ClientID}}.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("oauth2login: revoke: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oauth2login: revoke answered %d", resp.StatusCode)
	}
	return nil
}

// post is a token request: its answer, or its OAuth error.
func post(ctx context.Context, endpoint string, form url.Values) (*Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth2login: %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
		Description  string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("oauth2login: %s answered %d: %w", endpoint, resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK || out.AccessToken == "" {
		return nil, &Error{Code: out.Error, Description: out.Description, Status: resp.StatusCode}
	}
	r := &Result{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken, TokenType: out.TokenType}
	if out.ExpiresIn > 0 {
		r.Expiry = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	}
	return r, nil
}

// Error is the token endpoint's OAuth error: invalid_grant when a refresh
// token is no longer good, its login revoked or expired.
type Error struct {
	Code, Description string
	Status            int
}

func (e *Error) Error() string {
	return fmt.Sprintf("oauth2login: %s (%d): %s", e.Code, e.Status, e.Description)
}

func random(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// defaultOpenBrowser best-effort launches the OS's default browser.
func defaultOpenBrowser(u string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{u}
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", u}
	default:
		cmd, args = "xdg-open", []string{u}
	}
	// cmd is one of the three literals above, picked by runtime.GOOS
	// alone, and u is one argv entry, never through a shell
	return exec.Command(cmd, args...).Start() //#nosec G204
}
