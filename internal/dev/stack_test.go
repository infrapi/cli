package dev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/infrapi/cli/internal/clicontext"
	"github.com/infrapi/cli/internal/netproxy"
)

// recorder stands in for os/exec: it records the commands, and answers
// compose ps and kayra token mint.
type recorder struct {
	cmds    []Cmd
	running bool
}

func (r *recorder) exec(_ context.Context, c Cmd, _, _ io.Writer) (string, error) {
	r.cmds = append(r.cmds, c)
	line := strings.Join(c.Args, " ")
	switch {
	case strings.HasSuffix(line, "ps -q"):
		if r.running {
			return "abc123\n", nil
		}
		return "", nil
	case strings.HasSuffix(line, "config --services"):
		return "etcd\napisix\ngateway-init\nkayra\nreconciler\nhttpbin\n", nil
	case strings.Contains(line, "token mint"):
		return "token-for-" + line[strings.LastIndex(line, " ")+1:] + "\n", nil
	}
	return "", nil
}

func (r *recorder) lines() []string {
	var out []string
	for _, c := range r.cmds {
		out = append(out, strings.Join(c.Args, " "))
	}
	return out
}

func testStack(t *testing.T, devYAML string, o StackOptions) (*Stack, *recorder) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ConfigName), []byte(devYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	o.Config, o.Exec, o.Stdout, o.Stderr = filepath.Join(dir, ConfigName), rec.exec, io.Discard, io.Discard
	if o.Engine == "" {
		o.Engine = "podman"
	}
	s, err := OpenStack(o)
	if err != nil {
		t.Fatal(err)
	}
	// as dev init leaves it
	if _, err := s.Render(RenderOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	return s, rec
}

// Up ends on the accounts to log in with at Dex; none without it.
func TestUp_accounts(t *testing.T) {
	for _, idp := range []string{"", "idp: {enabled: false}\n"} {
		s, _ := testStack(t, dexUsers+idp, StackOptions{})
		var out strings.Builder
		s.stdout = &out
		if err := s.Up(context.Background(), RenderOptions{}); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if idp != "" {
			if strings.Contains(got, "PASSWORD") {
				t.Errorf("accounts without Dex:\n%s", got)
			}
			continue
		}
		for _, want := range []string{"Dex               http://localhost:9080/dex/.well-known/openid-configuration", "jdoe@example.com  jdoe      g", "ops@example.com   s3cret    -"} {
			if !strings.Contains(got, want) {
				t.Errorf("up printed no %q:\n%s", want, got)
			}
		}
	}
}

// Up recreates Kayra, its reconciler and gateway-init only when they were
// running and what they read changed.
func TestUp_recreatesOnlyOnChange(t *testing.T) {
	s, rec := testStack(t, "", StackOptions{})
	ctx := context.Background()
	recreate := "--force-recreate --no-deps kayra reconciler gateway-init dex"

	var out strings.Builder
	s.stdout = &out
	if err := s.Up(ctx, RenderOptions{}); err != nil { // fresh: nothing running yet
		t.Fatal(err)
	}
	// links to click on
	for _, want := range []string{"http://localhost:8081/.well-known/kayra.json", "http://localhost:9080/.well-known/kayra.json", "http://localhost:9180/ui/"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("up printed no %s:\n%s", want, out.String())
		}
	}
	s.stdout = io.Discard
	rec.running = true
	if err := s.Up(ctx, RenderOptions{}); err != nil { // running, nothing changed
		t.Fatal(err)
	}
	for _, l := range rec.lines() {
		if strings.Contains(l, recreate) {
			t.Fatalf("recreated without a change: %v", rec.lines())
		}
	}
	s.Config.Domain = "example.org"
	n := len(rec.lines())
	if err := s.Up(ctx, RenderOptions{}); !errors.Is(err, ErrRenderCanceled) {
		t.Fatalf("a change without --force: %v, want canceled", err)
	}
	if len(rec.lines()) != n {
		t.Fatalf("a canceled up ran %v", rec.lines()[n:])
	}
	if err := s.Up(ctx, RenderOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	last := rec.lines()[len(rec.lines())-1]
	if last != "podman compose --env-file .infrapi-dev/env -f .infrapi-dev/compose.yaml up -d "+recreate {
		t.Fatalf("after a change: %s", last)
	}
	if rec.cmds[0].Dir != s.Dir {
		t.Errorf("compose runs in %s, want the stack's dir %s", rec.cmds[0].Dir, s.Dir)
	}
}

// Update pulls every image and recreates every container; with kayra.src,
// it pulls the others, and builds Kayra's.
func TestUpdate(t *testing.T) {
	compose := "podman compose --env-file .infrapi-dev/env -f .infrapi-dev/compose.yaml "
	s, rec := testStack(t, "", StackOptions{})
	s.stdout = io.Discard
	if err := s.Update(context.Background(), RenderOptions{}); err != nil {
		t.Fatal(err)
	}
	want := []string{compose + "pull", compose + "ps -q", compose + "up -d --force-recreate"}
	if got := rec.lines(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("update ran\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module github.com/infrapi/kayra\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, rec = testStack(t, "kayra:\n  src: "+src+"\n", StackOptions{})
	s.stdout = io.Discard
	if err := s.Update(context.Background(), RenderOptions{}); err != nil {
		t.Fatal(err)
	}
	lines := rec.lines()
	if len(lines) != 5 || lines[1] != compose+"pull etcd apisix httpbin" || !strings.Contains(lines[2], "-t kayra-dev:local") ||
		lines[4] != compose+"up -d --force-recreate" {
		t.Fatalf("update with kayra.src ran\n%s", strings.Join(lines, "\n"))
	}
}

// With docker, compose gets compose.docker.yaml and the host is
// host.docker.internal; --compose replaces the command, not the files.
func TestCompose_engines(t *testing.T) {
	s, rec := testStack(t, "", StackOptions{Engine: "docker"})
	if err := s.Compose(context.Background(), "ps"); err != nil {
		t.Fatal(err)
	}
	if got := rec.lines()[0]; got != "docker compose --env-file .infrapi-dev/env -f .infrapi-dev/compose.yaml -f .infrapi-dev/compose.docker.yaml ps" {
		t.Errorf("docker: %s", got)
	}
	if s.HostAlias != "host.docker.internal" {
		t.Errorf("docker host alias: %s", s.HostAlias)
	}

	s, rec = testStack(t, "", StackOptions{Compose: "nerdctl compose", HostAlias: "host.lima.internal"})
	if err := s.Compose(context.Background(), "down", "-v"); err != nil {
		t.Fatal(err)
	}
	if got := rec.lines()[0]; got != "nerdctl compose --env-file .infrapi-dev/env -f .infrapi-dev/compose.yaml down -v" {
		t.Errorf("--compose: %s", got)
	}
	if s.HostAlias != "host.lima.internal" {
		t.Errorf("--host-alias: %s", s.HostAlias)
	}
}

// Contexts saves dev-admin, a sysadmin, current, and dev-<id> for every
// other user.
func TestContexts(t *testing.T) {
	t.Setenv("INFRAPI_CONTEXT_FILE", filepath.Join(t.TempDir(), "config"))
	s, rec := testStack(t, "users: [{id: admin, groups: [g]}, {id: jdoe, groups: []}]\nsysadmin_groups: [root]\n", StackOptions{})
	names, err := s.Contexts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "dev-jdoe,dev-admin" {
		t.Errorf("names: %v", names)
	}
	f, err := clicontext.Load()
	if err != nil {
		t.Fatal(err)
	}
	if f.CurrentContext != "dev-admin" || f.Contexts["dev-jdoe"].Token != "token-for-jdoe" ||
		f.Contexts["dev-admin"].Token != "token-for-root" || f.Contexts["dev-admin"].Server != "http://localhost:9080" {
		t.Errorf("contexts: %+v", f)
	}
	if !strings.Contains(strings.Join(rec.lines(), "\n"), "exec -T kayra kayra token mint --provider self --ttl 8h --group root") {
		t.Errorf("admin minted as: %v", rec.lines())
	}
}

// Build does nothing without kayra.src.
func TestBuild_withoutSrc(t *testing.T) {
	s, rec := testStack(t, "", StackOptions{})
	if err := s.Build(context.Background()); err != nil || len(rec.cmds) != 0 {
		t.Fatalf("err %v, commands %v", err, rec.lines())
	}
}

func port(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Health asks Kayra, at its own healthz, and the gateway.
func TestHealth(t *testing.T) {
	var asked []string
	ok := func(w http.ResponseWriter, r *http.Request) { asked = append(asked, r.URL.Path) }
	kayra, gateway := httptest.NewServer(http.HandlerFunc(ok)), httptest.NewServer(http.HandlerFunc(ok))
	defer kayra.Close()
	defer gateway.Close()
	s, _ := testStack(t, fmt.Sprintf("domain: example.org\nports: {kayra: %d, gateway: %d}\n", port(t, kayra), port(t, gateway)), StackOptions{})
	if err := s.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(asked, " ") != "/apis/system.example.org/v1/-/healthz /healthz" {
		t.Errorf("asked: %v", asked)
	}

	gateway.Close()
	if err := s.Health(context.Background()); err == nil || !strings.Contains(err.Error(), "gateway") {
		t.Errorf("gateway down: %v", err)
	}
}

// Routes puts every route of the stack, by id.
func TestRoutes(t *testing.T) {
	var put []string
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		put = append(put, r.Method+" "+strings.TrimPrefix(r.URL.Path, "/apisix/admin/routes/"))
	}))
	defer admin.Close()
	s, _ := testStack(t, fmt.Sprintf("ports: {admin: %d}\n", port(t, admin)), StackOptions{})
	if err := s.Routes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(put, ",") != "PUT kayra,PUT openapi-stub,PUT hooks,PUT healthz,PUT httpbin,PUT dex" {
		t.Errorf("put: %v", put)
	}
}

// OpenStack finds dev.yaml in a parent directory.
func TestOpenStack_findsConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ConfigName), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "internal", "compute")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	s, err := OpenStack(StackOptions{Engine: "podman"})
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(dir); s.Dir != dir && s.Dir != want {
		t.Errorf("dir %s, want %s", s.Dir, dir)
	}

	t.Chdir(t.TempDir())
	if _, err := OpenStack(StackOptions{}); err == nil {
		t.Error("no dev.yaml: no error")
	}
}

// podman compose runs without its provider banner, docker compose as is.
func TestCompose_podmanEnv(t *testing.T) {
	s, rec := testStack(t, "", StackOptions{})
	if err := s.Compose(context.Background(), "ps"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rec.cmds[0].Env, " "); got != "PODMAN_COMPOSE_WARNING_LOGS=false" {
		t.Errorf("podman env: %q", got)
	}
	s, rec = testStack(t, "", StackOptions{Engine: "docker"})
	if err := s.Compose(context.Background(), "ps"); err != nil {
		t.Fatal(err)
	}
	if len(rec.cmds[0].Env) != 0 {
		t.Errorf("docker env: %v", rec.cmds[0].Env)
	}
}

// A command running on the rendered files (ps, logs, down...) warns when
// dev.yaml changed since, and runs on them as they are; with none, it fails.
func TestCompose_onRenderedFiles(t *testing.T) {
	var errOut strings.Builder
	s, rec := testStack(t, "", StackOptions{})
	s.stderr = &errOut
	kayra := read(t, filepath.Join(s.Out, "kayra.yaml"))

	s.Config.Domain = "example.org"
	if err := s.Compose(context.Background(), "ps"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "warning: .infrapi-dev differs from dev.yaml (env, kayra.yaml") {
		t.Errorf("no warning: %q", errOut.String())
	}
	if read(t, filepath.Join(s.Out, "kayra.yaml")) != kayra {
		t.Error("ps rendered kayra.yaml")
	}
	if l := rec.lines(); len(l) != 1 || !strings.HasSuffix(l[0], " ps") {
		t.Errorf("ran %v", l)
	}

	s.Out = filepath.Join(t.TempDir(), ".infrapi-dev")
	if err := s.Compose(context.Background(), "ps"); err == nil || !strings.Contains(err.Error(), "dev up --force") {
		t.Errorf("not rendered: %v", err)
	}
}

// The build gets the proxy variables by name, its value never printed;
// doctor shows them, the password hidden.
func TestBuild_proxy(t *testing.T) {
	for _, v := range netproxy.Vars {
		t.Setenv(v, "")
	}
	t.Setenv("HTTPS_PROXY", "http://jdoe:secret@proxy.example.com:3128")
	t.Setenv("NO_PROXY", "localhost")
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, rec := testStack(t, "kayra: {src: "+src+"}\n", StackOptions{})
	if err := s.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	line := rec.lines()[0]
	if !strings.Contains(line, "--build-arg HTTPS_PROXY --build-arg NO_PROXY --build-context") || strings.Contains(line, "secret") {
		t.Errorf("build: %s", line)
	}
	var proxy string
	for _, c := range s.Doctor(context.Background()) {
		if c.Name == "proxy" {
			proxy = c.Detail
		}
	}
	if proxy != "HTTPS_PROXY=http://jdoe:xxxxx@proxy.example.com:3128 NO_PROXY=localhost" {
		t.Errorf("doctor: %q", proxy)
	}
}
