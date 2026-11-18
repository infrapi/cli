package dev

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"embed"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/pmezard/go-difflib/difflib"
	"gopkg.in/yaml.v3"
)

//go:embed templates
var templates embed.FS

// keyIndent is the indentation of a key in kayra.yaml's `secret: |` block.
const keyIndent = "          "

// Render renders the stack's files, from c, into dir:
//
//	env           the ports, image and domain, for compose (--env-file)
//	kayra.yaml    Kayra's configuration, the signing keys in it
//	users.yaml    the users of Kayra's local provider
//	routes/*.json the gateway's own routes, loaded by apisix/routes.sh
//	app.env       the API's settings, as infrapi/lib reads them (app.port...)
//	.gitignore    the whole directory, for git
//	keys/*.pem    the signing keys, generated when missing or when NewKeys
//	compose.yaml, compose.docker.yaml, apisix/config.yaml, apisix/routes.sh,
//	kayra/Dockerfile: the stack itself, from templates/stack, gateway.config
//	merged into apisix/config.yaml
//
// It writes nothing unless Force, or unless nothing changes: a render that
// would create, change or remove a file is canceled, Rendered.Pending set
// and Rendered.Diff holding what it would do, so a file edited by hand in
// dir is never overwritten without asking. Rendered.Kayra says whether
// what Kayra and gateway-init read changed, for dev up to recreate them.
func Render(c *Config, dir string, o RenderOptions) (Rendered, error) {
	pl, err := plan(c, dir, o.NewKeys)
	if err != nil {
		return Rendered{}, err
	}
	r := Rendered{Kayra: pl.kayra, Diff: pl.diff.String(), Files: pl.files, Keep: keep(pl.was, pl.writes)}
	if len(pl.files) > 0 && !o.Force {
		r.Pending = true
		return r, nil
	}
	return r, pl.apply(dir)
}

// RenderOptions are a render's options.
type RenderOptions struct {
	// NewKeys generates new signing keys.
	NewKeys bool
	// Force writes the changes. Without it, a render that would change a
	// file writes nothing.
	Force bool
}

// Rendered is what a render changed, or would change.
type Rendered struct {
	// Kayra is kayra.yaml, users.yaml, a route or a key: what Kayra, its
	// reconciler and gateway-init read when they start.
	Kayra bool
	// Diff is the diff of the files changed, relative to the render's dir,
	// empty when none did: a new file is named only, a signing key shown
	// as its fingerprint.
	Diff string
	// Files are the files changed, relative to the render's dir.
	Files []string
	// Pending is a render canceled, without Force: nothing was written.
	Pending bool
	// Keep is what to add to dev.yaml for the render to give the files as
	// they are on disk, edits made by hand included: kayra.config,
	// gateway.routes, compose.env, app.env. Keys are left out.
	Keep map[string]any
}

// rendering is what a render writes: the files' new contents, nil for a
// route to remove.
type rendering struct {
	writes map[string][]byte
	// was is what a file changed held, for Rendered.Keep
	was   map[string][]byte
	keys  map[string][]byte
	files []string
	diff  strings.Builder
	kayra bool
}

// plan is what rendering c into dir would change, without writing it.
func plan(c *Config, dir string, newKeys bool) (*rendering, error) {
	pl := &rendering{writes: map[string][]byte{}, keys: map[string][]byte{}, was: map[string][]byte{}}
	keys := map[string][]byte{}
	for _, k := range [][2]string{{"self.pem", c.Kayra.Keys.Self}, {"local.pem", c.Kayra.Keys.Local}} {
		name, own := k[0], k[1]
		rel := filepath.Join("keys", name)
		path := filepath.Join(dir, rel)
		var key []byte
		var created bool
		var err error
		if own != "" {
			key, created, err = ownKey(c, own, path)
			if created {
				fmt.Fprintf(&pl.diff, "%s: dev.yaml's kayra.keys.%s, %s\n", rel, strings.TrimSuffix(name, ".pem"), fingerprint(key))
			}
		} else {
			key, created, err = signingKey(path, newKeys)
			if created {
				fmt.Fprintf(&pl.diff, "%s: new signing key %s\n", rel, fingerprint(key))
			}
		}
		if err != nil {
			return nil, err
		}
		if created {
			pl.keys[rel] = key
			pl.files = append(pl.files, rel)
			pl.kayra = true
		}
		keys[name] = key
	}

	kayra, err := renderKayra(c, keys["self.pem"], keys["local.pem"])
	if err != nil {
		return nil, err
	}
	users, err := yaml.Marshal(map[string][]User{"users": c.Users})
	if err != nil {
		return nil, err
	}
	routes, removed, err := stackRoutes(c)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{
		"env":        []byte(withEnv(envFile(c), c.Compose.Env, "compose.env")),
		"kayra.yaml": kayra,
		"users.yaml": append([]byte("# The users of dev.yaml, rendered by infrapi-cli dev render: edit dev.yaml.\n"), users...),
		"app.env":    []byte(withEnv(appEnv(c), c.App.Env, "app.env")),
		// git ignores the directory: it is rendered, and has keys
		".gitignore": []byte("# Rendered by infrapi-cli dev render: the whole directory is.\n*\n"),
	}
	if err := stackFiles(c, files); err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	for i, r := range routes {
		body, err := json.MarshalIndent(r.Body, "", "  ")
		if err != nil {
			return nil, err
		}
		name := fmt.Sprintf("%02d-%s.json", i, r.ID)
		files[filepath.Join("routes", name)] = append(body, '\n')
		keep[name] = true
	}
	// the routes dev.yaml removes: apisix/routes.sh deletes them
	if len(removed) > 0 {
		files[filepath.Join("routes", removedRoutes)] = []byte(strings.Join(removed, "\n") + "\n")
		keep[removedRoutes] = true
	}

	// a route dev.yaml no longer has, or a file put there by hand
	old, err := os.ReadDir(filepath.Join(dir, "routes"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, e := range old {
		if keep[e.Name()] {
			continue
		}
		rel := filepath.Join("routes", e.Name())
		was, err := os.ReadFile(filepath.Join(dir, rel)) //#nosec G304 -- a file of the render's own dir
		if err != nil {
			return nil, err
		}
		pl.diff.WriteString(unifiedDiff(rel, was, nil))
		pl.writes[rel] = nil
		pl.was[filepath.ToSlash(rel)] = was
		pl.files = append(pl.files, rel)
		pl.kayra = true
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		was, err := os.ReadFile(filepath.Join(dir, name)) //#nosec G304 -- a file of the render's own dir
		switch {
		case errors.Is(err, fs.ErrNotExist):
			fmt.Fprintf(&pl.diff, "%s: new file\n", name)
		case err != nil:
			return nil, err
		case bytes.Equal(was, files[name]):
			continue
		default:
			pl.diff.WriteString(unifiedDiff(name, was, files[name]))
			pl.was[filepath.ToSlash(name)] = was
		}
		pl.writes[name] = files[name]
		pl.files = append(pl.files, name)
		if name != "env" && name != "app.env" && name != ".gitignore" {
			pl.kayra = true
		}
	}
	return pl, nil
}

// apply writes pl into dir.
func (pl *rendering) apply(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, "keys"), 0o700); err != nil {
		return err
	}
	// The containers read the files as another user than their owner:
	// they are 0644, and dir, 0700, keeps them from the host's other users.
	if err := os.Chmod(dir, 0o700); err != nil { //#nosec G302 -- a directory: 0700 is its owner only
		return err
	}
	// read by the containers as another user than their owner
	for _, sub := range []string{"routes", "apisix", "kayra"} {
		d := filepath.Join(dir, sub)
		if err := os.MkdirAll(d, 0o755); err != nil { //#nosec G301 -- see above
			return err
		}
		// MkdirAll and WriteFile apply the umask: 077 would leave 0700 and 0600
		if err := os.Chmod(d, 0o755); err != nil { //#nosec G302 -- see above
			return err
		}
	}
	for rel, key := range pl.keys {
		if err := os.WriteFile(filepath.Join(dir, rel), key, 0o600); err != nil {
			return err
		}
	}
	for rel, content := range pl.writes {
		path := filepath.Join(dir, rel)
		if content == nil {
			if err := os.Remove(path); err != nil {
				return err
			}
			continue
		}
		if err := writeFile(path, content); err != nil {
			return err
		}
	}
	// files left as they were, written under another umask by an older render
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Dir(path) == filepath.Join(dir, "keys") {
			return err
		}
		return os.Chmod(path, 0o644) //#nosec G302 -- see writeFile
	})
}

// writeFile writes content to path, 0644 whatever the umask: it is mounted
// into the containers and read there as another user than its owner, the
// render's dir (0700) keeping it from the host's other users.
func writeFile(path string, content []byte) error {
	if err := os.WriteFile(path, content, 0o644); err != nil { //#nosec G306 -- see above
		return err
	}
	return os.Chmod(path, 0o644) //#nosec G302 -- see above
}

// unifiedDiff is the diff of the file name from was to now, nil for a file
// missing on that side. A PEM block is shown as its fingerprint: the diff
// never prints a signing key.
func unifiedDiff(name string, was, now []byte) string {
	from, to := "a/"+name, "b/"+name
	if was == nil {
		from = "/dev/null"
	}
	if now == nil {
		to = "/dev/null"
	}
	lines := func(b []byte) []string {
		if b == nil {
			return nil
		}
		return difflib.SplitLines(redactPEM(string(b)))
	}
	out, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        lines(was),
		B:        lines(now),
		FromFile: from,
		ToFile:   to,
		Context:  3,
	})
	if err != nil {
		return fmt.Sprintf("%s: %v\n", name, err)
	}
	return out
}

// pemBlock is a PEM block, as kayra.yaml indents it.
var pemBlock = regexp.MustCompile(`(?m)^([ \t]*)-----BEGIN ([A-Z ]+)-----\n(?:[ \t]*[A-Za-z0-9+/=]*\n)*?[ \t]*-----END [A-Z ]+-----$`)

func redactPEM(s string) string {
	return pemBlock.ReplaceAllStringFunc(s, func(block string) string {
		m := pemBlock.FindStringSubmatch(block)
		return fmt.Sprintf("%s-----%s %s-----", m[1], m[2], fingerprint([]byte(block)))
	})
}

// fingerprint is a key's short SHA-256, whitespace ignored.
func fingerprint(key []byte) string {
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(string(key)), "")))
	return "sha256:" + hex.EncodeToString(sum[:])[:16]
}

// stackFiles adds the stack's own files to files, as templates/stack has
// them, dev.yaml's gateway.config merged into apisix/config.yaml.
func stackFiles(c *Config, files map[string][]byte) error {
	return fs.WalkDir(templates, "templates/stack", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := templates.ReadFile(path)
		if err != nil {
			return err
		}
		rel := filepath.FromSlash(strings.TrimPrefix(path, "templates/stack/"))
		if rel == filepath.Join("apisix", "config.yaml") {
			if data, err = mergeYAML(data, &c.Gateway.Config); err != nil {
				return fmt.Errorf("apisix/config.yaml: %w", err)
			}
		}
		files[rel] = data
		return nil
	})
}

// mergeYAML is the YAML document data, over merged into it as nodes: its
// comments stay.
func mergeYAML(data []byte, over *yaml.Node) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if over.Kind == yaml.MappingNode {
		mergeNode(doc.Content[0], over)
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	return out.Bytes(), enc.Close()
}

// removedRoutes is the file, in routes/, of the ids of the routes dev.yaml
// removes, one a line.
const removedRoutes = "removed"

// ownKey is dev.yaml's kayra.keys file own, a PEM key, relative to
// dev.yaml; created says it is not yet the one at path.
func ownKey(c *Config, own, path string) (key []byte, created bool, err error) {
	if !filepath.IsAbs(own) {
		own = filepath.Join(c.dir, own)
	}
	key, err = os.ReadFile(own) //#nosec G304 -- a key file dev.yaml names
	if err != nil {
		return nil, false, fmt.Errorf("kayra.keys: %w", err)
	}
	if b, _ := pem.Decode(key); b == nil {
		return nil, false, fmt.Errorf("kayra.keys: %s: not a PEM key", own)
	}
	cur, err := os.ReadFile(path) //#nosec G304 -- a key of the render's own dir
	return key, err != nil || !bytes.Equal(cur, key), nil
}

// signingKey is the PEM of the RSA key at path, generated when the file is
// missing or when renew: created says so, and the render writes it.
func signingKey(path string, renew bool) (key []byte, created bool, err error) {
	if !renew {
		data, err := os.ReadFile(path) //#nosec G304 -- a key of the render's own dir
		if err == nil {
			if b, _ := pem.Decode(data); b == nil {
				return nil, false, fmt.Errorf("%s: not a PEM key: remove it, or render with new keys", path)
			}
			return data, false, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, false, err
		}
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, false, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		return nil, false, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), true, nil
}

func renderKayra(c *Config, selfKey, localKey []byte) ([]byte, error) {
	tmpl, err := template.New("kayra.yaml.tmpl").Delims("[[", "]]").ParseFS(templates, "templates/kayra.yaml.tmpl")
	if err != nil {
		return nil, err
	}
	indent := func(key []byte) string {
		lines := strings.Split(strings.TrimRight(string(key), "\n"), "\n")
		return keyIndent + strings.Join(lines, "\n"+keyIndent)
	}
	var out bytes.Buffer
	err = tmpl.Execute(&out, struct {
		*Config
		SelfKey, LocalKey string
	}{c, indent(selfKey), indent(localKey)})
	if err != nil {
		return nil, err
	}
	// dev.yaml's kayra.config, merged in as YAML nodes: the template's
	// comments stay
	merged, err := mergeYAML(out.Bytes(), &c.Kayra.Config)
	if err != nil {
		return nil, fmt.Errorf("kayra.yaml: %w", err)
	}
	return merged, nil
}

// envFile holds what compose.yaml and the Makefile take from dev.yaml:
// KEY=value lines, no quotes, read by both.
func envFile(c *Config) string {
	image, pull := c.Kayra.Image, "missing"
	if c.Kayra.Src != "" {
		image, pull = localImage, "never"
	}
	vars := [][2]string{
		{"DEV_DOMAIN", c.Domain},
		{"DEV_PLATFORM", c.Platform},
		{"KAYRA_IMAGE", image},
		{"KAYRA_PULL_POLICY", pull},
		{"KAYRA_SRC", c.Kayra.Src},
		{"KAYRA_HEALTHZ", "/apis/" + c.Builtin("system") + "/v1/-/healthz"},
		{"KAYRA_PORT", fmt.Sprint(c.Ports.Kayra)},
		{"GATEWAY_PORT", fmt.Sprint(c.Ports.Gateway)},
		{"GATEWAY_TLS_PORT", fmt.Sprint(c.Ports.GatewayTLS)},
		{"ADMIN_PORT", fmt.Sprint(c.Ports.Admin)},
		{"PROMETHEUS_PORT", fmt.Sprint(c.Ports.Prometheus)},
		{"CONTROL_PORT", fmt.Sprint(c.Ports.Control)},
		{"ETCD_PORT", fmt.Sprint(c.Ports.Etcd)},
		{"SYSADMIN_GROUP", c.SysadminGroups[0]},
	}
	var b strings.Builder
	b.WriteString("# Rendered by infrapi-cli dev render from dev.yaml: edit dev.yaml.\n")
	for _, kv := range vars {
		fmt.Fprintf(&b, "%s=%s\n", kv[0], kv[1])
	}
	return b.String()
}

// localImage is the image make build builds from kayra.src.
const localImage = "kayra-dev:local"

// appEnv is the API's dotenv, as infrapi-cli app init's internal/config
// reads it: infrapi/lib's INFRAPI_APP_* keys, and Kayra's URL through the
// gateway, as <NAME>_AUTH_URL.
func appEnv(c *Config) string {
	name := AppName(c.FullAPI(c.App.API))
	gateway := fmt.Sprintf("http://localhost:%d", c.Ports.Gateway)
	return strings.Join([]string{
		"# The API's settings, rendered by infrapi-cli dev render from dev.yaml's app.",
		"INFRAPI_APP_NAME=" + q(name),
		"INFRAPI_APP_PLATFORM=" + q(c.Platform),
		"INFRAPI_APP_REGION=" + q(c.Region),
		"INFRAPI_APP_LOCATION=" + q(c.Location),
		"INFRAPI_APP_FQDN=" + q(name+".localhost.local"),
		"INFRAPI_APP_URL=" + q(gateway),
		"# 0.0.0.0: APISIX, in a container, reaches it through host.containers.internal",
		"INFRAPI_APP_LISTEN_ADDRESS=" + q(fmt.Sprintf("0.0.0.0:%d", c.App.Port)),
		`INFRAPI_APP_LOG_LEVEL="info"`,
		`INFRAPI_APP_TRUSTED_PROXIES=[]`,
		`INFRAPI_APP_CORS_ALLOW_ORIGINS=["*"]`,
		`INFRAPI_APP_CORS_ALLOW_METHODS=["GET","POST","PUT","PATCH","DELETE","HEAD","OPTIONS"]`,
		`INFRAPI_APP_CORS_ALLOW_HEADERS=["Origin","Content-Type","Authorization"]`,
		`INFRAPI_APP_CORS_EXPOSE_HEADERS=["Content-Length"]`,
		`INFRAPI_APP_CORS_ALLOW_CREDENTIALS="true"`,
		`INFRAPI_APP_CORS_MAX_AGE="12"`,
		"",
		"# Kayra, through the gateway",
		EnvPrefix(name) + "_AUTH_URL=" + q(gateway),
		"",
	}, "\n")
}

// AppName is an API's short name, its group's first label: compute for
// compute.infrapi.io.
func AppName(group string) string {
	name, _, _ := strings.Cut(group, ".")
	return name
}

// EnvPrefix is the prefix of an API's own settings: COMPUTE for compute,
// MY_API for my-api.
func EnvPrefix(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

func q(s string) string { return `"` + s + `"` }

// route is one APISIX route: its id, and the body PUT to the Admin API.
type route struct {
	ID   string
	Body map[string]any
}

// gatewayRoutes are the stack's own routes, in the order they are loaded.
func gatewayRoutes(c *Config) ([]route, error) {
	stub, err := templates.ReadFile("templates/openapi-stub.lua")
	if err != nil {
		return nil, err
	}
	mock := func(body string) map[string]any {
		return map[string]any{"mocking": map[string]any{
			"response_status": 200, "content_type": "application/json", "response_example": body,
		}}
	}
	return []route{
		{"kayra", map[string]any{
			"uris": []string{
				"/apis", "/oauth2/*", "/.well-known/kayra.json", "/.well-known/api.json",
				"/apis/" + c.Builtin("system") + "/*",
				"/apis/" + c.Builtin("catalog") + "/*",
				"/apis/" + c.Builtin("authorization") + "/*",
			},
			"upstream": map[string]any{"type": "roundrobin", "nodes": map[string]int{"kayra:8081": 1}},
		}},
		{"openapi-stub", map[string]any{
			"uri":     "/apis/*",
			"vars":    [][]string{{"uri", "~~", `^/apis/[a-z0-9.-]+/v[0-9a-z]+/-/openapi\.json$`}},
			"methods": []string{"GET"},
			"plugins": map[string]any{"serverless-pre-function": map[string]any{
				"phase": "access", "functions": []string{string(stub)},
			}},
		}},
		{"hooks", map[string]any{"uri": "/hooks/*", "plugins": mock(`{"status":"done"}`)}},
		{"healthz", map[string]any{"uri": "/healthz", "methods": []string{"GET"}, "plugins": mock(`{"status":"ok"}`)}},
		{"httpbin", map[string]any{
			"uri":      "/httpbin/*",
			"plugins":  map[string]any{"proxy-rewrite": map[string]any{"regex_uri": []string{"^/httpbin/(.*)", "/$1"}}},
			"upstream": map[string]any{"type": "roundrobin", "nodes": map[string]int{"httpbin:80": 1}},
		}},
	}, nil
}
