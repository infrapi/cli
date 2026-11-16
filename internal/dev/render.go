package dev

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"embed"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

//go:embed templates
var templates embed.FS

// keyIndent is the indentation of a key in kayra.yaml's `secret: |` block.
const keyIndent = "          "

// Render writes the stack's files, from c, into dir:
//
//	env           the ports, image and domain, for compose (--env-file)
//	kayra.yaml    Kayra's configuration, the signing keys in it
//	users.yaml    the users of Kayra's local provider
//	routes/*.json the gateway's own routes, loaded by apisix/routes.sh
//	example.env   the example API's settings
//	keys/*.pem    the signing keys, generated when missing or when newKeys
//
// A file is written only when its content changes: Rendered says whether
// what Kayra and gateway-init read changed, for dev up to recreate them.
func Render(c *Config, dir string, newKeys bool) (Rendered, error) {
	var r Rendered
	err := render(c, dir, newKeys, &r)
	return r, err
}

// Rendered is what a render changed.
type Rendered struct {
	// Kayra is kayra.yaml, users.yaml or a route: what Kayra, its
	// reconciler and gateway-init read when they start.
	Kayra bool
}

func render(c *Config, dir string, newKeys bool, r *Rendered) error {
	if err := os.MkdirAll(filepath.Join(dir, "keys"), 0o700); err != nil {
		return err
	}
	// The containers read the files as another user than their owner:
	// they are 0644, and dir, 0700, keeps them from the host's other users.
	if err := os.Chmod(dir, 0o700); err != nil { //#nosec G302 -- a directory: 0700 is its owner only
		return err
	}

	selfKey, err := signingKey(filepath.Join(dir, "keys", "self.pem"), newKeys)
	if err != nil {
		return err
	}
	localKey, err := signingKey(filepath.Join(dir, "keys", "local.pem"), newKeys)
	if err != nil {
		return err
	}

	kayra, err := renderKayra(c, selfKey, localKey)
	if err != nil {
		return err
	}
	users, err := yaml.Marshal(map[string][]User{"users": c.Users})
	if err != nil {
		return err
	}
	routes, err := gatewayRoutes(c)
	if err != nil {
		return err
	}

	files := map[string][]byte{
		"env":         []byte(envFile(c)),
		"kayra.yaml":  kayra,
		"users.yaml":  append([]byte("# The users of dev.yaml, rendered by infrapi-cli dev render: edit dev.yaml.\n"), users...),
		"example.env": []byte(exampleEnv(c)),
	}
	routesDir := filepath.Join(dir, "routes")
	// read by gateway-init as another user than its owner; dir, 0700, keeps
	// it from the host's other users
	if err := os.MkdirAll(routesDir, 0o755); err != nil { //#nosec G301 -- see above
		return err
	}
	// MkdirAll and WriteFile apply the umask: 077 would leave 0700 and 0600
	if err := os.Chmod(routesDir, 0o755); err != nil { //#nosec G302 -- see above
		return err
	}
	keep := map[string]bool{}
	for i, r := range routes {
		body, err := json.MarshalIndent(r.Body, "", "  ")
		if err != nil {
			return err
		}
		name := fmt.Sprintf("%02d-%s.json", i, r.ID)
		files[filepath.Join("routes", name)] = append(body, '\n')
		keep[name] = true
	}
	// a route dev.yaml no longer has
	old, err := os.ReadDir(routesDir)
	if err != nil {
		return err
	}
	for _, e := range old {
		if !keep[e.Name()] {
			if err := os.Remove(filepath.Join(routesDir, e.Name())); err != nil {
				return err
			}
			r.Kayra = true
		}
	}
	for name, content := range files {
		changed, err := writeIfChanged(filepath.Join(dir, name), content)
		if err != nil {
			return err
		}
		if changed && name != "env" && name != "example.env" {
			r.Kayra = true
		}
	}
	return nil
}

// writeIfChanged writes content to path, unless path holds it already, and
// says whether it wrote it. The file is 0644 either way, whatever the umask:
// it is mounted into the containers and read there as another user than its
// owner, the render's dir (0700) keeping it from the host's other users.
func writeIfChanged(path string, content []byte) (bool, error) {
	changed := true
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, content) { //#nosec G304 -- a file of the render's own dir
		changed = false
	} else if err := os.WriteFile(path, content, 0o644); err != nil { //#nosec G306 -- see above
		return false, err
	}
	return changed, os.Chmod(path, 0o644) //#nosec G302 -- see above
}

// signingKey is the PEM of the RSA key at path, generated (and written,
// 0600) when the file is missing or when renew.
func signingKey(path string, renew bool) ([]byte, error) {
	if !renew {
		data, err := os.ReadFile(path) //#nosec G304 -- a key of the render's own dir
		if err == nil {
			if b, _ := pem.Decode(data); b == nil {
				return nil, fmt.Errorf("%s: not a PEM key: remove it, or render with new keys", path)
			}
			return data, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return data, os.WriteFile(path, data, 0o600)
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
	return out.Bytes(), err
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
		{"EXAMPLE_API", c.FullAPI(c.Example.API)},
		{"EXAMPLE_PORT", fmt.Sprint(c.Example.Port)},
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

// exampleEnv is the example API's dotenv: infrapi/lib's INFRAPI_APP_*
// keys, its group and Kayra's URL, through the gateway.
func exampleEnv(c *Config) string {
	name, _, _ := strings.Cut(c.FullAPI(c.Example.API), ".")
	gateway := fmt.Sprintf("http://localhost:%d", c.Ports.Gateway)
	return strings.Join([]string{
		"# The example API's settings, rendered by infrapi-cli dev render from dev.yaml.",
		"INFRAPI_APP_NAME=" + q(name),
		"INFRAPI_APP_PLATFORM=" + q(c.Platform),
		"INFRAPI_APP_REGION=" + q(c.Region),
		"INFRAPI_APP_LOCATION=" + q(c.Location),
		"INFRAPI_APP_FQDN=" + q(name+".localhost.local"),
		"INFRAPI_APP_URL=" + q(gateway),
		"# 0.0.0.0: APISIX, in a container, reaches it through host.containers.internal",
		"INFRAPI_APP_LISTEN_ADDRESS=" + q(fmt.Sprintf("0.0.0.0:%d", c.Example.Port)),
		`INFRAPI_APP_LOG_LEVEL="info"`,
		`INFRAPI_APP_TRUSTED_PROXIES=[]`,
		`INFRAPI_APP_CORS_ALLOW_ORIGINS=["*"]`,
		`INFRAPI_APP_CORS_ALLOW_METHODS=["GET","POST","PUT","PATCH","DELETE","HEAD","OPTIONS"]`,
		`INFRAPI_APP_CORS_ALLOW_HEADERS=["Origin","Content-Type","Authorization"]`,
		`INFRAPI_APP_CORS_EXPOSE_HEADERS=["Content-Length"]`,
		`INFRAPI_APP_CORS_ALLOW_CREDENTIALS="true"`,
		`INFRAPI_APP_CORS_MAX_AGE="12"`,
		"",
		"# its API group, and Kayra, through the gateway",
		"COMPUTE_API_GROUP=" + q(c.FullAPI(c.Example.API)),
		"COMPUTE_AUTH_URL=" + q(gateway),
		"",
	}, "\n")
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
