package dev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/infrapi/cli/internal/clicontext"
)

// ConfigName is the stack's settings file, looked for in the working
// directory, then in its parents.
const ConfigName = "dev.yaml"

// StackOptions open a stack. Every field is optional.
type StackOptions struct {
	// Config is the path of dev.yaml; when empty, ConfigName is looked for
	// in the working directory and its parents.
	Config string
	// Engine runs the containers: podman or docker. When empty, podman if
	// it is on PATH, docker otherwise.
	Engine string
	// Compose replaces the compose command, "nerdctl compose" say. The
	// stack's --env-file and -f files are added to it. "<engine> compose"
	// when empty.
	Compose string
	// Build replaces the image build command. "podman build --format
	// docker" or "docker build" when empty.
	Build string
	// HostAlias is the host's name in the containers, where the gateway
	// reaches an API running on it. host.containers.internal with podman,
	// host.docker.internal with docker, when empty.
	HostAlias string
	// Stdout and Stderr get the commands' output; os.Stdout and os.Stderr
	// when nil.
	Stdout, Stderr io.Writer
	// Exec runs a command; os/exec when nil. Tests record instead.
	Exec ExecFunc
	// HTTP calls Kayra and the gateway; a 10s client when nil.
	HTTP *http.Client
}

// Cmd is one command the stack runs.
type Cmd struct {
	// Dir is where it runs.
	Dir string
	// Env is added to the process's environment.
	Env []string
	// Args are the program and its arguments.
	Args []string
	// Capture returns the command's stdout instead of streaming it.
	Capture bool
}

// ExecFunc runs c, streaming its output to stdout and stderr, or returning
// its stdout when c.Capture.
type ExecFunc func(ctx context.Context, c Cmd, stdout, stderr io.Writer) (string, error)

// Stack is a dev stack: its dev.yaml, its directory, and how its
// containers run.
type Stack struct {
	Config     *Config
	ConfigPath string
	// Dir is the stack's directory: dev.yaml's, compose.yaml's.
	Dir string
	// Out is where the stack's files are rendered: Dir/.infrapi-dev.
	Out       string
	Engine    string
	HostAlias string

	compose, build []string
	stdout, stderr io.Writer
	exec           ExecFunc
	http           *http.Client
}

// OpenStack loads the stack's dev.yaml and settles how its containers run.
func OpenStack(o StackOptions) (*Stack, error) {
	path := o.Config
	if path == "" {
		found, err := findConfig()
		if err != nil {
			return nil, err
		}
		path = found
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	c, err := LoadConfig(abs)
	if err != nil {
		return nil, err
	}
	s := &Stack{
		Config: c, ConfigPath: abs, Dir: filepath.Dir(abs), Out: filepath.Join(filepath.Dir(abs), ".infrapi-dev"),
		Engine: o.Engine, HostAlias: o.HostAlias,
		stdout: o.Stdout, stderr: o.Stderr, exec: o.Exec, http: o.HTTP,
	}
	if s.Engine == "" {
		s.Engine = "docker"
		if _, err := exec.LookPath("podman"); err == nil {
			s.Engine = "podman"
		}
	}
	if s.HostAlias == "" {
		s.HostAlias = "host.containers.internal"
		if s.Engine == "docker" {
			s.HostAlias = "host.docker.internal"
		}
	}
	s.compose = strings.Fields(o.Compose)
	if len(s.compose) == 0 {
		s.compose = []string{s.Engine, "compose"}
	}
	s.build = strings.Fields(o.Build)
	if len(s.build) == 0 {
		s.build = []string{s.Engine, "build"}
		if s.Engine == "podman" {
			// keeps the image's HEALTHCHECK, which podman's OCI default drops
			s.build = append(s.build, "--format", "docker")
		}
	}
	if s.stdout == nil {
		s.stdout = os.Stdout
	}
	if s.stderr == nil {
		s.stderr = os.Stderr
	}
	if s.exec == nil {
		s.exec = osExec
	}
	if s.http == nil {
		s.http = &http.Client{Timeout: 10 * time.Second}
	}
	return s, nil
}

// findConfig looks for ConfigName in the working directory and its parents.
func findConfig() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		p := filepath.Join(dir, ConfigName)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no %s here or above: run it in a stack (infrapi-cli dev init creates one), or give --config", ConfigName)
		}
		dir = parent
	}
}

// osExec runs c with os/exec, printing its command line first.
func osExec(ctx context.Context, c Cmd, stdout, stderr io.Writer) (string, error) {
	_, _ = fmt.Fprintf(stderr, "+ %s\n", strings.Join(c.Args, " "))
	cmd := exec.CommandContext(ctx, c.Args[0], c.Args[1:]...) //#nosec G204 -- the stack's own commands, from its flags and dev.yaml
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stderr = stderr
	if c.Capture {
		var out strings.Builder
		cmd.Stdout = &out
		err := cmd.Run()
		return out.String(), err
	}
	cmd.Stdout = stdout
	cmd.Stdin = os.Stdin
	return "", cmd.Run()
}

func (s *Stack) run(ctx context.Context, c Cmd) error {
	if c.Dir == "" {
		c.Dir = s.Dir
	}
	_, err := s.exec(ctx, c, s.stdout, s.stderr)
	return err
}

func (s *Stack) output(ctx context.Context, c Cmd) (string, error) {
	if c.Dir == "" {
		c.Dir = s.Dir
	}
	c.Capture = true
	out, err := s.exec(ctx, c, s.stdout, s.stderr)
	return strings.TrimSpace(out), err
}

// composeArgs is the compose command with the stack's env and files.
func (s *Stack) composeArgs(args ...string) []string {
	a := append([]string{}, s.compose...)
	a = append(a, "--env-file", filepath.Join(".infrapi-dev", "env"), "-f", "compose.yaml")
	if s.Engine == "docker" {
		// docker on Linux has no host.docker.internal of its own
		a = append(a, "-f", "compose.docker.yaml")
	}
	return append(a, args...)
}

// composeEnv is compose's environment: podman compose, which runs an
// external provider (podman-compose, docker-compose), says so on every call
// unless told not to.
func (s *Stack) composeEnv() []string {
	if s.Engine == "podman" {
		return []string{"PODMAN_COMPOSE_WARNING_LOGS=false"}
	}
	return nil
}

// Compose runs compose with args, the stack rendered first.
func (s *Stack) Compose(ctx context.Context, args ...string) error {
	if _, err := s.Render(false); err != nil {
		return err
	}
	return s.run(ctx, Cmd{Args: s.composeArgs(args...), Env: s.composeEnv()})
}

// Render renders the stack's files into Out.
func (s *Stack) Render(newKeys bool) (Rendered, error) {
	r, err := Render(s.Config, s.Out, newKeys)
	if err != nil {
		return r, err
	}
	return r, s.readable()
}

// mounted are the stack's own files compose.yaml mounts into the
// containers, besides the rendered ones.
var mounted = []string{filepath.Join("apisix", "config.yaml"), filepath.Join("apisix", "routes.sh")}

// readable makes the files the containers mount readable by them: they run
// as another user than the files' owner (kayra as 10001, apisix as its
// own), and dev init wrote them under the umask, 0600 with a umask of 077.
func (s *Stack) readable() error {
	for _, f := range mounted {
		p := filepath.Join(s.Dir, f)
		st, err := os.Stat(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
		mode := st.Mode().Perm() | 0o444 // read for all: compose.yaml mounts it read-only
		if mode != st.Mode().Perm() {
			if err := os.Chmod(p, mode); err != nil { //#nosec G302 -- the stack's own config and script, mounted into its containers
				return err
			}
		}
	}
	return nil
}

// Build builds localImage from kayra.src; it says so and does nothing
// without one.
func (s *Stack) Build(ctx context.Context) error {
	src := s.Config.Kayra.Src
	if src == "" {
		_, _ = fmt.Fprintf(s.stdout, "build: kayra.src is not set in %s: the stack runs %s, nothing to build\n", ConfigName, s.Config.Kayra.Image)
		return nil
	}
	if !filepath.IsAbs(src) {
		src = filepath.Join(s.Dir, src)
	}
	if _, err := os.Stat(filepath.Join(src, "go.mod")); err != nil {
		return fmt.Errorf("build: no Kayra checkout at %s (kayra.src in %s)", src, ConfigName)
	}
	args := append(append([]string{}, s.build...), "--build-context", "src="+src, "-t", localImage, "kayra")
	return s.run(ctx, Cmd{Args: args})
}

// Up renders, builds when kayra.src is set, and starts the stack. Kayra, its
// reconciler and gateway-init are recreated when what they read changed
// since they started: compose recreates a service whose image, ports or
// environment changed, not one whose mounted file did.
func (s *Stack) Up(ctx context.Context, newKeys bool) error {
	r, err := s.Render(newKeys)
	if err != nil {
		return err
	}
	if s.Config.Kayra.Src != "" {
		if err := s.Build(ctx); err != nil {
			return err
		}
	}
	// any container of the stack: podman-compose's ps takes no service name
	running, err := s.output(ctx, Cmd{Args: s.composeArgs("ps", "-q"), Env: s.composeEnv()})
	if err != nil {
		running = ""
	}
	if err := s.run(ctx, Cmd{Args: s.composeArgs("up", "-d"), Env: s.composeEnv()}); err != nil {
		return err
	}
	if running != "" && r.Kayra {
		_, _ = fmt.Fprintf(s.stdout, "up: %s changed: recreating kayra, reconciler and gateway-init\n", ConfigName)
		if err := s.run(ctx, Cmd{Args: s.composeArgs("up", "-d", "--force-recreate", "--no-deps", "kayra", "reconciler", "gateway-init"), Env: s.composeEnv()}); err != nil {
			return err
		}
	}
	p := s.Config.Ports
	_, _ = fmt.Fprintf(s.stdout, "\n  Kayra   http://localhost:%d   gateway http://localhost:%d   APISIX Admin API http://localhost:%d\n  next:   infrapi-cli dev contexts\n",
		p.Kayra, p.Gateway, p.Admin)
	return nil
}

// ResetKayra wipes Kayra's objects from etcd, its routes and keys kept,
// then restarts Kayra and its reconciler.
func (s *Stack) ResetKayra(ctx context.Context) error {
	if err := s.Compose(ctx, "exec", "-T", "etcd", "etcdctl", "del", "--prefix", "/kayra"); err != nil {
		return err
	}
	return s.run(ctx, Cmd{Args: s.composeArgs("restart", "kayra", "reconciler"), Env: s.composeEnv()})
}

// Mint is a token from Kayra's own signing keys: kayra token mint, in the
// kayra container, for 8 hours. provider is self (groups as given) or local
// (groups from dev.yaml's users).
func (s *Stack) Mint(ctx context.Context, provider, subject string, groups ...string) (string, error) {
	if _, err := s.Render(false); err != nil {
		return "", err
	}
	args := []string{"exec", "-T", "kayra", "kayra", "token", "mint", "--provider", provider, "--ttl", "8h"}
	if subject != "" {
		args = append(args, "--subject", subject)
	}
	for _, g := range groups {
		args = append(args, "--group", g)
	}
	token, err := s.output(ctx, Cmd{Args: s.composeArgs(args...), Env: s.composeEnv()})
	if err != nil {
		return "", fmt.Errorf("mint a token: %w (is the stack up? infrapi-cli dev up)", err)
	}
	if token == "" {
		return "", errors.New("mint a token: kayra token mint printed nothing")
	}
	return token, nil
}

// Contexts saves infrapi-cli contexts on the gateway: dev-admin, a
// sysadmin, the current one, and dev-<id> for every other user of dev.yaml.
func (s *Stack) Contexts(ctx context.Context) ([]string, error) {
	gateway := fmt.Sprintf("http://localhost:%d", s.Config.Ports.Gateway)
	type entry struct{ name, token string }
	var entries []entry
	for _, u := range s.Config.Users {
		if u.ID == "admin" {
			continue
		}
		t, err := s.Mint(ctx, "local", u.ID)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry{"dev-" + u.ID, t})
	}
	admin, err := s.Mint(ctx, "self", "", s.Config.SysadminGroups[0])
	if err != nil {
		return nil, err
	}
	entries = append(entries, entry{"dev-admin", admin}) // last: the current one

	f, err := clicontext.Load()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		f.SetContext(e.name, clicontext.Context{Server: gateway, Token: e.token, TokenType: "Bearer"})
		names = append(names, e.name)
	}
	return names, f.Save()
}

// Health checks Kayra, directly, and the gateway answer.
func (s *Stack) Health(ctx context.Context) error {
	p := s.Config.Ports
	var failed []string
	for _, check := range []struct{ name, url string }{
		{"kayra", fmt.Sprintf("http://localhost:%d/apis/%s/v1/-/healthz", p.Kayra, s.Config.Builtin("system"))},
		{"gateway", fmt.Sprintf("http://localhost:%d/healthz", p.Gateway)},
	} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, check.url, nil)
		if err != nil {
			return err
		}
		resp, err := s.http.Do(req)
		status := "unreachable"
		if err == nil {
			_ = resp.Body.Close()
			status = resp.Status
			if resp.StatusCode == http.StatusOK {
				_, _ = fmt.Fprintf(s.stdout, "  ok       %-8s %s\n", check.name, check.url)
				continue
			}
		}
		_, _ = fmt.Fprintf(s.stdout, "  FAILING  %-8s %s: %s\n", check.name, check.url, status)
		failed = append(failed, check.name)
	}
	if len(failed) > 0 {
		return fmt.Errorf("health: %s not answering: infrapi-cli dev ps, dev logs", strings.Join(failed, ", "))
	}
	return nil
}

// Routes puts the stack's own routes back into the gateway, as rendered.
func (s *Stack) Routes(ctx context.Context) error {
	routes, err := gatewayRoutes(s.Config)
	if err != nil {
		return err
	}
	g := s.Gateway()
	for _, r := range routes {
		if err := g.PutRoute(ctx, r.ID, r.Body); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(s.stdout, "routes: %s\n", r.ID)
	}
	return nil
}

// Gateway is the stack's APISIX Admin API.
func (s *Stack) Gateway() *Gateway {
	return &Gateway{URL: fmt.Sprintf("http://localhost:%d", s.Config.Ports.Admin), Key: AdminKey, HTTP: s.http}
}

// Example runs the example API on the host, with its rendered settings,
// against libSrc, a lib checkout, when set.
func (s *Stack) Example(ctx context.Context, dir, libSrc string) error {
	if _, err := s.Render(false); err != nil {
		return err
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(s.Dir, dir)
	}
	env := []string{"INFRAPI_CONFIG_DOTENV_FILE=" + filepath.Join(s.Out, "example.env")}
	if libSrc != "" {
		lib, err := filepath.Abs(libSrc)
		if err != nil {
			return err
		}
		work := filepath.Join(s.Out, "go.work")
		if err := os.Remove(work); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := s.run(ctx, Cmd{Dir: s.Out, Args: []string{"go", "work", "init", dir, lib}}); err != nil {
			return err
		}
		env = append(env, "GOWORK="+work)
	}
	return s.run(ctx, Cmd{Dir: dir, Env: env, Args: []string{"go", "run", "."}})
}

// Check lints the stack's script and builds the example.
func (s *Stack) Check(ctx context.Context, dir string) error {
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(s.Dir, dir)
	}
	for _, c := range []Cmd{
		{Args: []string{"sh", "-n", filepath.Join("apisix", "routes.sh")}},
		{Dir: dir, Args: []string{"go", "vet", "./..."}},
		{Dir: dir, Args: []string{"go", "build", "-o", os.DevNull, "."}},
	} {
		if err := s.run(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

// Check is one line of Doctor.
type Check struct {
	Name     string
	OK       bool
	Optional bool
	// Detail is the version found, or how to get it.
	Detail string
}

// Doctor checks the prerequisites: the engine, its compose, and go for the
// example API.
func (s *Stack) Doctor(ctx context.Context) []Check {
	version := func(args ...string) (string, bool) {
		if _, err := exec.LookPath(args[0]); err != nil {
			return "", false
		}
		out, err := s.exec(ctx, Cmd{Dir: s.Dir, Args: args, Capture: true}, io.Discard, io.Discard)
		if err != nil {
			return "", false
		}
		line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
		return line, true
	}
	var checks []Check
	if v, ok := version(s.Engine, "--version"); ok {
		checks = append(checks, Check{Name: s.Engine, OK: true, Detail: v})
	} else {
		checks = append(checks, Check{Name: s.Engine, Detail: "install podman (https://podman.io/docs/installation) or docker, or set --engine"})
	}
	if v, ok := version(append(append([]string{}, s.compose...), "version")...); ok {
		checks = append(checks, Check{Name: "compose", OK: true, Detail: v})
	} else {
		checks = append(checks, Check{Name: "compose", Detail: "podman-compose or docker-compose (podman), the compose plugin (docker), or set --compose"})
	}
	if v, ok := version("go", "version"); ok {
		checks = append(checks, Check{Name: "go", OK: true, Optional: true, Detail: v})
	} else {
		checks = append(checks, Check{Name: "go", Optional: true, Detail: "only for dev example and dev check: https://go.dev/dl/"})
	}
	return checks
}
