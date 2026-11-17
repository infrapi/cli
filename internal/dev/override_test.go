package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const overrides = `
kayra:
  config:
    reconciler:
      max_concurrent_reconciles: 50   # mine
      default_mode: null
      mode:
        dev:
          - method: get
            endpoint: "http://host.containers.internal:9000/sync"
    logging: {level: debug}
gateway:
  routes:
    hooks:
      plugins:
        mocking:
          response_example: '{"status":"failed"}'
    mine:
      uri: /mine/*
      upstream: {type: roundrobin, nodes: {"host.containers.internal:9000": 1}}
    httpbin: null
example:
  env:
    INFRAPI_APP_LOG_LEVEL: debug
    MY_FLAG: "a b"
compose:
  env:
    KAYRA_PORT: "18081"
    APISIX_IMAGE: apache/apisix:3.12.0-debian
`

func renderOverrides(t *testing.T, extra string) (*Config, string) {
	t.Helper()
	c, err := LoadConfig(writeConfig(t, overrides+extra))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), ".infrapi-dev")
	if _, err := Render(c, dir, RenderOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	return c, dir
}

// kayra.config is merged into kayra.yaml: a map key by key, a list
// replaced, null removing the key; the template's comments stay.
func TestOverride_kayraConfig(t *testing.T) {
	_, dir := renderOverrides(t, "")
	text := read(t, filepath.Join(dir, "kayra.yaml"))
	var k struct {
		Reconciler map[string]any `yaml:"reconciler"`
		Logging    map[string]any `yaml:"logging"`
	}
	if err := yaml.Unmarshal([]byte(text), &k); err != nil {
		t.Fatal(err)
	}
	if k.Reconciler["max_concurrent_reconciles"] != 50 {
		t.Errorf("max_concurrent_reconciles = %v", k.Reconciler["max_concurrent_reconciles"])
	}
	if _, ok := k.Reconciler["default_mode"]; ok {
		t.Error("default_mode: null left it")
	}
	if k.Reconciler["gateway_url"] != "http://apisix:9080" {
		t.Errorf("a key the override does not name is gone: %v", k.Reconciler)
	}
	dev := k.Reconciler["mode"].(map[string]any)["dev"].([]any)
	if len(dev) != 1 || dev[0].(map[string]any)["method"] != "get" {
		t.Errorf("the list is not replaced: %v", dev)
	}
	if k.Logging["level"] != "debug" {
		t.Errorf("a new key: %v", k.Logging)
	}
	for _, want := range []string{"# The stack's APISIX", "max_concurrent_reconciles: 50 # mine", "-----BEGIN PRIVATE KEY-----"} {
		if !strings.Contains(text, want) {
			t.Errorf("kayra.yaml misses %q", want)
		}
	}
}

// gateway.routes merges into a route by id, adds a new id, and null
// removes one, listed in routes/removed for routes.sh to delete.
func TestOverride_routes(t *testing.T) {
	_, dir := renderOverrides(t, "")
	entries, err := os.ReadDir(filepath.Join(dir, "routes"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if got := strings.Join(names, " "); got != "00-kayra.json 01-openapi-stub.json 02-hooks.json 03-healthz.json 04-mine.json removed" {
		t.Fatalf("routes: %s", got)
	}
	if got := read(t, filepath.Join(dir, "routes", "removed")); got != "httpbin\n" {
		t.Errorf("removed = %q", got)
	}
	var hooks map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join(dir, "routes", "02-hooks.json"))), &hooks); err != nil {
		t.Fatal(err)
	}
	mocking := hooks["plugins"].(map[string]any)["mocking"].(map[string]any)
	if mocking["response_example"] != `{"status":"failed"}` || mocking["response_status"] != float64(200) || hooks["uri"] != "/hooks/*" {
		t.Errorf("hooks: %v", hooks)
	}
	if !strings.Contains(read(t, filepath.Join(dir, "routes", "04-mine.json")), `"host.containers.internal:9000": 1`) {
		t.Error("mine's upstream")
	}
}

// dev routes puts the merged routes and deletes the removed ones.
func TestOverride_routesCommand(t *testing.T) {
	var calls []string
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+strings.TrimPrefix(r.URL.Path, "/apisix/admin/routes/"))
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNotFound) // already gone: no error
		}
	}))
	defer admin.Close()
	s, _ := testStack(t, overrides+fmt.Sprintf("ports: {admin: %d}\n", port(t, admin)), StackOptions{})
	if err := s.Routes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "PUT kayra,PUT openapi-stub,PUT hooks,PUT healthz,PUT mine,DELETE httpbin" {
		t.Errorf("calls: %s", got)
	}
}

// example.env and compose.env replace a variable in place, and add the
// others after, quoted when needed.
func TestOverride_env(t *testing.T) {
	_, dir := renderOverrides(t, "")
	example := read(t, filepath.Join(dir, "example.env"))
	for _, want := range []string{"INFRAPI_APP_LOG_LEVEL=debug\n", "# dev.yaml's example.env\nMY_FLAG=\"a b\"\n"} {
		if !strings.Contains(example, want) {
			t.Errorf("example.env misses %q:\n%s", want, example)
		}
	}
	if strings.Contains(example, `INFRAPI_APP_LOG_LEVEL="info"`) {
		t.Error("example.env keeps the replaced value")
	}
	env := read(t, filepath.Join(dir, "env"))
	for _, want := range []string{"KAYRA_PORT=18081\n", "# dev.yaml's compose.env\nAPISIX_IMAGE=apache/apisix:3.12.0-debian\n"} {
		if !strings.Contains(env, want) {
			t.Errorf("env misses %q:\n%s", want, env)
		}
	}
}

// kayra.keys takes key files of your own, relative to dev.yaml.
func TestOverride_keys(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := signingKey(filepath.Join(t.TempDir(), "x"), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.dir, "mine.pem"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	c.Kayra.Keys.Self = "mine.pem"
	dir := t.TempDir()
	r, err := Render(c, dir, RenderOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(dir, "keys", "self.pem")) != string(key) {
		t.Error("keys/self.pem is not mine.pem")
	}
	if !strings.Contains(r.Diff, "keys/self.pem: dev.yaml's kayra.keys.self, sha256:") {
		t.Errorf("diff: %s", r.Diff)
	}
	if r, err = Render(c, dir, RenderOptions{}); err != nil || r.Pending {
		t.Fatalf("same key again: %+v %v", r, err)
	}
	c.Kayra.Keys.Local = "missing.pem"
	if _, err := Render(c, dir, RenderOptions{}); err == nil || !strings.Contains(err.Error(), "kayra.keys") {
		t.Errorf("a missing key file: %v", err)
	}
}

// dev.yaml's overrides are checked when it loads.
func TestOverride_invalid(t *testing.T) {
	for content, want := range map[string]string{
		"kayra: {config: [a]}\n":                   "kayra.config: a map",
		"gateway: {routes: {My.Route: {}}}\n":      "gateway.routes.My.Route: a route id",
		"gateway: {routes: {mine: [a]}}\n":         "gateway.routes.mine: a route",
		"example: {env: {1BAD: x}}\n":              "example.env.1BAD: an environment variable name",
		"compose: {env: {OK: \"a\\nb\"}}\n":        "compose.env.OK: one line",
		"kayra: {config: {a: 1}, nope: true}\n":    "field nope not found",
		"gateway: {routes: {kayra: null}, x: 1}\n": "field x not found",
	} {
		if _, err := LoadConfig(writeConfig(t, content)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", content, err, want)
		}
	}
}

// A canceled render says what to add to dev.yaml to keep the files as they
// are: an edit made by hand under the dev.yaml key it belongs to.
func TestOverride_keep(t *testing.T) {
	c, dir := renderOverrides(t, "")
	edit := func(name, from, to string) {
		p := filepath.Join(dir, name)
		s := read(t, p)
		if !strings.Contains(s, from) {
			t.Fatalf("%s has no %q", name, from)
		}
		if err := os.WriteFile(p, []byte(strings.Replace(s, from, to, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	edit("kayra.yaml", "max_concurrent_reconciles: 50", "max_concurrent_reconciles: 7")
	edit("kayra.yaml", "gateway_url: \"http://apisix:9080\"", "gateway_url: \"http://apisix:9081\"")
	edit("example.env", "INFRAPI_APP_LOG_LEVEL=debug", "INFRAPI_APP_LOG_LEVEL=warn")
	edit(filepath.Join("routes", "03-healthz.json"), `"uri": "/healthz"`, `"uri": "/health"`)
	if err := os.WriteFile(filepath.Join(dir, "routes", "09-extra.json"), []byte(`{"uri": "/extra"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := Render(c, dir, RenderOptions{})
	if err != nil || !r.Pending {
		t.Fatalf("%+v %v", r, err)
	}
	got := keepYAML(r.Keep, "")
	for _, want := range []string{
		"example:\n  env:\n    INFRAPI_APP_LOG_LEVEL: warn\n",
		"gateway:\n  routes:\n    extra:\n      uri: /extra\n    healthz:\n      uri: /health\n",
		"kayra:\n  config:\n    reconciler:\n      gateway_url: http://apisix:9081\n      max_concurrent_reconciles: 7\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("keep misses\n%s\nin\n%s", want, got)
		}
	}
	if strings.Contains(got, "PRIVATE KEY") {
		t.Error("keep has a key")
	}

	// added to dev.yaml, the render gives what the files had, renumbered
	// and reformatted maybe
	var add map[string]any
	if err := yaml.Unmarshal([]byte(got), &add); err != nil {
		t.Fatal(err)
	}
	merged, _ := yaml.Marshal(mergeAny(yamlMap(t, overrides), add))
	c2, err := LoadConfig(writeConfig(t, string(merged)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render(c2, dir, RenderOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	k := read(t, filepath.Join(dir, "kayra.yaml"))
	for _, want := range []string{"max_concurrent_reconciles: 7", "gateway_url: http://apisix:9081"} {
		if !strings.Contains(k, want) {
			t.Errorf("kayra.yaml misses %q", want)
		}
	}
	if !strings.Contains(read(t, filepath.Join(dir, "example.env")), "INFRAPI_APP_LOG_LEVEL=warn\n") {
		t.Error("example.env")
	}
	routes := map[string]string{}
	entries, _ := os.ReadDir(filepath.Join(dir, "routes"))
	for _, e := range entries {
		routes[e.Name()] = read(t, filepath.Join(dir, "routes", e.Name()))
	}
	for name, want := range map[string]string{"03-healthz.json": `"uri": "/health"`, "04-extra.json": `"uri": "/extra"`, "05-mine.json": `"uri": "/mine/*"`} {
		if !strings.Contains(routes[name], want) {
			t.Errorf("routes/%s misses %q: %v", name, want, routes)
		}
	}
}

func yamlMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// compose.override.yaml, when the stack has one, is added to compose's
// files: compose does not read it by itself once -f is given.
func TestOverride_composeFile(t *testing.T) {
	s, rec := testStack(t, "", StackOptions{})
	if err := os.WriteFile(filepath.Join(s.Dir, composeOverride), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Compose(context.Background(), "ps"); err != nil {
		t.Fatal(err)
	}
	if got := rec.lines()[0]; got != "podman compose --env-file .infrapi-dev/env -f .infrapi-dev/compose.yaml -f compose.override.yaml ps" {
		t.Errorf("compose: %s", got)
	}
}

// The stack's own files are rendered into .infrapi-dev/, compose.yaml's
// paths relative to it; gateway.config is merged into APISIX's config.
func TestRender_stackFiles(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, "gateway:\n  config:\n    apisix: {enable_ipv6: true}\n    plugin_attr: null\n"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := Render(c, dir, RenderOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	compose := read(t, filepath.Join(dir, "compose.yaml"))
	for _, want := range []string{"./kayra.yaml:/etc/kayra/kayra.yaml", "./routes:/routes", "./apisix/config.yaml:", "./apisix/routes.sh:"} {
		if !strings.Contains(compose, want) {
			t.Errorf("compose.yaml misses %q", want)
		}
	}
	if strings.Contains(compose, "./.infrapi-dev/") {
		t.Error("compose.yaml has a path from the stack's root")
	}
	for _, f := range []string{"compose.docker.yaml", "apisix/routes.sh", "kayra/Dockerfile"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Error(err)
		}
	}
	apisix := read(t, filepath.Join(dir, "apisix", "config.yaml"))
	var a map[string]any
	if err := yaml.Unmarshal([]byte(apisix), &a); err != nil {
		t.Fatal(err)
	}
	if a["apisix"].(map[string]any)["enable_ipv6"] != true || a["apisix"].(map[string]any)["node_listen"] != 9080 {
		t.Errorf("apisix: %v", a["apisix"])
	}
	if _, ok := a["plugin_attr"]; ok {
		t.Error("plugin_attr: null left it")
	}
	if !strings.Contains(apisix, "key: "+AdminKey) || !strings.Contains(apisix, "# A development key") {
		t.Error("config.yaml lost its admin key or its comments")
	}

	// edited by hand: the keep snippet is gateway.config
	p := filepath.Join(dir, "apisix", "config.yaml")
	if err := os.WriteFile(p, []byte(strings.Replace(apisix, "node_listen: 9080", "node_listen: 9090", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Render(c, dir, RenderOptions{})
	if err != nil || !r.Pending {
		t.Fatalf("%+v %v", r, err)
	}
	if got := keepYAML(r.Keep, ""); got != "gateway:\n  config:\n    apisix:\n      node_listen: 9090\n" {
		t.Errorf("keep = %q", got)
	}
}
