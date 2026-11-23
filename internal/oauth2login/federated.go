package oauth2login

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// JWTBearer is RFC 7523's client assertion type: the client authenticates
// with a JWT, here its platform's identity token.
const JWTBearer = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer" //#nosec G101 -- a URN naming a grant, no credential

// ServiceAccount is a service account's client id at Kayra, from
// <namespace>/<name>: system:serviceaccount:<namespace>:<name>.
func ServiceAccount(nsName string) (string, error) {
	ns, name, ok := strings.Cut(nsName, "/")
	if !ok || ns == "" || name == "" || strings.Contains(name, "/") {
		return "", fmt.Errorf("service account %q: <namespace>/<name>, like core/deployer", nsName)
	}
	return "system:serviceaccount:" + ns + ":" + name, nil
}

// FederatedLogin gets a token of clientID, a service account, from
// server's authorization server (client_credentials): the assertion is
// the platform's identity token that source gives (IDToken), for Kayra's
// issuer as its audience. No refresh token: a new token is asked the same
// way.
func FederatedLogin(ctx context.Context, server, clientID, source string) (*Result, error) {
	m, err := discover(ctx, server)
	if err != nil {
		return nil, err
	}
	assertion, err := IDToken(ctx, source, m.Issuer)
	if err != nil {
		return nil, err
	}
	return post(ctx, m.TokenEndpoint, url.Values{
		"grant_type":            {"client_credentials"},
		"client_id":             {clientID},
		"client_assertion_type": {JWTBearer},
		"client_assertion":      {assertion},
		"resource":              {strings.TrimRight(server, "/")},
	})
}

// IDToken is the platform identity token source names, for audience:
//
//   - env:NAME, the variable NAME: a GitLab CI job's id_tokens entry,
//     whose aud the job sets;
//   - file:PATH, the file PATH: a projected Kubernetes service account
//     token, say;
//   - github, asked of GitHub Actions for audience (the job needs
//     permissions: id-token: write).
func IDToken(ctx context.Context, source, audience string) (string, error) {
	kind, arg, _ := strings.Cut(source, ":")
	switch kind {
	case "env":
		v := strings.TrimSpace(os.Getenv(arg))
		if arg == "" || v == "" {
			return "", fmt.Errorf("--id-token %s: the variable %q is empty", source, arg)
		}
		return v, nil
	case "file":
		data, err := os.ReadFile(arg) //#nosec G304 -- the file the user names, their platform's token
		if err != nil {
			return "", fmt.Errorf("--id-token %s: %w", source, err)
		}
		return strings.TrimSpace(string(data)), nil
	case "github":
		return githubIDToken(ctx, audience)
	}
	return "", fmt.Errorf("--id-token %q: env:<NAME>, file:<PATH> or github", source)
}

// githubIDToken asks GitHub Actions for the job's OIDC token, for
// audience: ACTIONS_ID_TOKEN_REQUEST_URL with ACTIONS_ID_TOKEN_REQUEST_TOKEN,
// set when the job has permissions: id-token: write.
func githubIDToken(ctx context.Context, audience string) (string, error) {
	endpoint, bearer := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL"), os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	if endpoint == "" || bearer == "" {
		return "", fmt.Errorf("--id-token github: ACTIONS_ID_TOKEN_REQUEST_URL is unset: run in a GitHub Actions job with permissions: id-token: write")
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("--id-token github: ACTIONS_ID_TOKEN_REQUEST_URL: %w", err)
	}
	q := u.Query()
	q.Set("audience", audience)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil) //#nosec G704 -- GitHub Actions' own token endpoint, from the job's environment
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := httpClient.Do(req) //#nosec G704 -- see above
	if err != nil {
		return "", fmt.Errorf("--id-token github: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Value string `json:"value"`
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("--id-token github: GitHub answered %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil || out.Value == "" {
		return "", fmt.Errorf("--id-token github: no token in GitHub's answer")
	}
	return out.Value, nil
}
