package main

// What infrapi-cli learns from the server instead of being configured:
// Kayra's own endpoints and API domain, from /.well-known/kayra.json, and
// every api group's full name, from GET /apis — the way kubectl resolves
// a resource through discovery rather than a local setting.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/infrapi/cli/internal/clicontext"
)

// kayraDocumentPath mirrors discovery.Path (kept local so the CLI only
// depends on the wire format, not the server's packages).
const kayraDocumentPath = "/.well-known/kayra.json"

// kayraDocument mirrors discovery.Document.
type kayraDocument struct {
	Domain    string            `json:"domain"`
	APIs      map[string]string `json:"apis"`
	Endpoints struct {
		Userinfo string `json:"userinfo"`
		Check    string `json:"check"`
		JWKS     string `json:"jwks"`
		APIs     string `json:"apis"`
	} `json:"endpoints"`
}

// fetchKayra reads c's server's discovery document. It needs no token.
func fetchKayra(ctx context.Context, c clicontext.Context) (kayraDocument, error) {
	var doc kayraDocument
	docURL := strings.TrimRight(c.Server, "/") + kayraDocumentPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, docURL, nil)
	if err != nil {
		return doc, fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return doc, fmt.Errorf("call %s: %w", docURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return doc, fmt.Errorf("read %s: %w", docURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		return doc, fmt.Errorf("%s returned %d: is %s a Kayra server?", docURL, resp.StatusCode, c.Server)
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return doc, fmt.Errorf("decode %s: %w", docURL, err)
	}
	if doc.Endpoints.Check == "" {
		return doc, fmt.Errorf("%s names no check endpoint", docURL)
	}
	return doc, nil
}

// builtinGroup is the full name of Kayra's built-in group name (system,
// catalog, authorization) per doc, "" when doc has no such group.
func (doc kayraDocument) builtinGroup(name string) string {
	path, ok := doc.APIs[name]
	if !ok {
		return ""
	}
	// /apis/<group>/v1
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}
