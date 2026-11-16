package dev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Kayra calls Kayra's API, at Server (the gateway, or Kayra itself) with
// Token, a sysadmin's.
type Kayra struct {
	Server string
	Token  string
	HTTP   *http.Client

	// the roots of Kayra's own groups, from its /.well-known/kayra.json
	system, catalog, authorization string
}

// discover reads Kayra's discovery document: the roots of its groups, and
// its domain, which must be dev.yaml's.
func (k *Kayra) discover(ctx context.Context, domain string) error {
	var doc struct {
		Domain string            `json:"domain"`
		APIs   map[string]string `json:"apis"`
	}
	if _, err := k.do(ctx, http.MethodGet, "/.well-known/kayra.json", nil, &doc); err != nil {
		return fmt.Errorf("discover Kayra at %s: %w", k.Server, err)
	}
	if doc.Domain != domain {
		return fmt.Errorf("kayra at %s has domain %q, dev.yaml %q: make up after changing dev.yaml", k.Server, doc.Domain, domain)
	}
	k.system, k.catalog, k.authorization = doc.APIs["system"], doc.APIs["catalog"], doc.APIs["authorization"]
	if k.system == "" || k.catalog == "" || k.authorization == "" {
		return fmt.Errorf("kayra at %s: its discovery document misses a group: %v", k.Server, doc.APIs)
	}
	return nil
}

// do sends body as JSON, decodes the answer into out when not nil, and
// returns the status; anything but 2xx is an error, but 404 for a GET,
// which is returned as is.
func (k *Kayra) do(ctx context.Context, method, path string, body, out any) (int, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(k.Server, "/")+path, r)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if k.Token != "" {
		req.Header.Set("Authorization", "Bearer "+k.Token)
	}
	client := k.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if method == http.MethodGet && resp.StatusCode == http.StatusNotFound {
		return resp.StatusCode, nil
	}
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, fmt.Errorf("%s %s: %w", method, path, err)
		}
	}
	return resp.StatusCode, nil
}

// Apply creates dev.yaml's APIs and tenants in Kayra, then syncs every API.
// It can be run again: Kayra's creates are upserts, and a subscription
// that exists already is left as it is, not reset to init. log gets a
// line per API and tenant.
func Apply(ctx context.Context, c *Config, k *Kayra, log io.Writer) error {
	if err := k.discover(ctx, c.Domain); err != nil {
		return err
	}
	post := func(path string, body any) error {
		_, err := k.do(ctx, http.MethodPost, path, body, nil)
		return err
	}

	for _, a := range c.APIs {
		full := c.FullAPI(a.Name)
		short, _, _ := strings.Cut(full, ".")
		if err := post(k.catalog+"/apiregistrations", map[string]any{
			"name": full, "short_name": short, "version": a.Version, "owner": a.Owner,
		}); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(log, "apply: api %s %s\n", full, a.Version)
	}

	for _, t := range c.Tenants {
		if err := applyTenant(ctx, c, k, t, post); err != nil {
			return fmt.Errorf("tenant %s: %w", t.Name, err)
		}
		_, _ = fmt.Fprintf(log, "apply: tenant %s (%s)\n", t.Name, strings.Join(c.Subscribed(t), ", "))
	}

	for _, a := range c.APIs {
		full := c.FullAPI(a.Name)
		if _, err := k.do(ctx, http.MethodPut, k.catalog+"/apiregistrations/"+full+"/sync", map[string]any{}, nil); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintf(log, "apply: %d APIs synced, the reconciler carries the subscriptions out\n", len(c.APIs))
	return nil
}

func applyTenant(ctx context.Context, c *Config, k *Kayra, t Tenant, post func(string, any) error) error {
	ns := t.Name
	meta := map[string]string{"namespace": ns, "region": c.Region, "platform": c.Platform}
	nsMeta := map[string]string{"region": c.Region, "platform": c.Platform}
	nsRoot := k.authorization + "/namespaces/" + ns

	steps := []struct {
		path string
		body map[string]any
	}{
		{k.system + "/namespaces", map[string]any{"name": ns}},
		// cluster RBAC: the tenant's admins manage its catalog and its RBAC
		{k.authorization + "/clustergroups", map[string]any{"name": ns + "-admins", "metadata": meta, "groups": t.Admins}},
		{k.authorization + "/clusterroles", map[string]any{"name": ns + "-admin-role", "metadata": meta, "rules": []map[string]any{{
			"apis": []string{c.Builtin("catalog"), c.Builtin("authorization")}, "resources": []string{"*"}, "verbs": []string{"*"},
		}}}},
		{k.authorization + "/clusterrolebindings", map[string]any{
			"name": ns + "-admin-binding", "metadata": meta, "role": ns + "-admin-role", "groups": []string{ns + "-admins"},
		}},
		// in the namespace: read-write on every API
		{nsRoot + "/groups", map[string]any{"name": ns + "-group-rw", "metadata": meta, "groups": t.Admins}},
		{nsRoot + "/roles", map[string]any{
			"name": ns + "-role-rw", "description": ns + " rw role", "metadata": nsMeta,
			"rules": []map[string]any{{"apis": []string{"*"}, "resources": []string{"*"}, "verbs": []string{"*"}}},
		}},
		{nsRoot + "/rolebindings", map[string]any{
			"name": ns + "-binding-rw", "description": "default binding for rw", "metadata": nsMeta,
			"role": ns + "-role-rw", "groups": []string{ns + "-group-rw"},
		}},
	}
	for _, s := range steps {
		if err := post(s.path, s.body); err != nil {
			return err
		}
	}

	subs := k.catalog + "/namespaces/" + ns + "/subscriptions"
	for _, api := range c.Subscribed(t) {
		// a new subscription starts at init: create it only once
		status, err := k.do(ctx, http.MethodGet, subs+"/"+api, nil, nil)
		if err != nil {
			return err
		}
		if status == http.StatusNotFound {
			if err := post(subs, map[string]any{"api": api}); err != nil {
				return err
			}
		}
	}
	return nil
}
