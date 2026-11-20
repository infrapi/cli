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
	"slices"
	"strings"
	"time"

	"github.com/infrapi/cli/internal/netproxy"

	"github.com/infrapi/cli/internal/ui"

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
	// Dir is the stack's directory: dev.yaml's.
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
	_, _ = fmt.Fprintln(ui.Writer(stderr), ui.Faint("+ "+strings.Join(c.Args, " ")))
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
	out := filepath.Base(s.Out)
	// compose's project directory is the first file's, .infrapi-dev: its
	// paths, and compose.override.yaml's, are relative to it
	a = append(a, "--env-file", filepath.Join(out, "env"), "-f", filepath.Join(out, "compose.yaml"))
	if s.Engine == "docker" {
		// docker on Linux has no host.docker.internal of its own
		a = append(a, "-f", filepath.Join(out, "compose.docker.yaml"))
	}
	// yours: compose does not read it by itself once -f is given
	if _, err := os.Stat(filepath.Join(s.Dir, composeOverride)); err == nil {
		a = append(a, "-f", composeOverride)
	}
	return append(a, args...)
}

// composeOverride is the stack's own compose file, beside dev.yaml, when
// it has one: what the rendered compose.yaml does not cover, a service
// added say. Its relative paths are .infrapi-dev's, compose's project
// directory.
const composeOverride = "compose.override.yaml"

// composeEnv is compose's environment: podman compose, which runs an
// external provider (podman-compose, docker-compose), says so on every call
// unless told not to.
func (s *Stack) composeEnv() []string {
	if s.Engine == "podman" {
		return []string{"PODMAN_COMPOSE_WARNING_LOGS=false"}
	}
	return nil
}

// Compose runs compose with args, on the stack's files as rendered.
func (s *Stack) Compose(ctx context.Context, args ...string) error {
	if err := s.rendered(); err != nil {
		return err
	}
	return s.run(ctx, Cmd{Args: s.composeArgs(args...), Env: s.composeEnv()})
}

// ErrRenderCanceled is a render that would change a file, without
// --force: nothing was written.
var ErrRenderCanceled = errors.New("render canceled: nothing written, see above")

// Render renders the stack's files into Out, and prints the diff of what
// changed. Without o.Force, a render that would change a file writes
// nothing: it prints the diff, how to make the change in dev.yaml, and
// returns ErrRenderCanceled.
func (s *Stack) Render(o RenderOptions) (Rendered, error) {
	r, err := Render(s.Config, s.Out, o)
	if err != nil {
		return r, err
	}
	if r.Diff != "" {
		_ = ui.Highlight(s.stdout, "diff", r.Diff)
	}
	if r.Pending {
		s.explain(r)
		return r, ErrRenderCanceled
	}
	return r, nil
}

// sources says where each rendered file comes from: what to change in
// dev.yaml instead of the file.
var sources = []struct{ prefix, from string }{
	{"kayra.yaml", "domain, platform, region, location, sysadmin_groups, ports.kayra, users; anything else: kayra.config"},
	{"users.yaml", "users"},
	{"env", "domain, platform, kayra.image or kayra.src, ports; anything else: compose.env"},
	{"app.env", "domain, platform, region, location, ports.gateway, app; anything else: app.env"},
	{"routes/", "domain, ports; a route changed, added or removed (null): gateway.routes.<id>. Your API's route: infrapi-cli dev api add"},
	{"keys/", "kayra.keys.self, kayra.keys.local for keys of your own; --new-keys for new ones"},
	{"apisix/config.yaml", "gateway.config"},
	{"dex/", "users (password, attributes.email and name), ports.gateway, ports.kayra; idp.enabled: false for none"},
	{"compose.yaml", "compose.config; or a compose.override.yaml beside dev.yaml, read after it"},
	{"compose.docker.yaml", "none: it is infrapi-cli's; a compose.override.yaml beside dev.yaml is read after it"},
	{"apisix/routes.sh", "none: it is infrapi-cli's; mount a script of your own from compose.override.yaml"},
	{"kayra/Dockerfile", "none: it is infrapi-cli's"},
}

// explain says why a render was canceled, and how to go on.
func (s *Stack) explain(r Rendered) {
	out := ui.Writer(s.stderr)
	_, _ = fmt.Fprintf(out, "\n%s nothing written to %s: it differs from what %s renders, the diff above.\n\n",
		ui.Warn("render canceled:"), s.Out, ConfigName)
	_, _ = fmt.Fprintf(out, "  The files in %s are rendered from %s: change %s, not them. Each file, from %s's:\n",
		ui.Name(filepath.Base(s.Out)), ConfigName, ui.Name(ConfigName), ConfigName)
	seen := map[string]bool{}
	for _, f := range r.Files {
		f = filepath.ToSlash(f)
		for _, src := range sources {
			if (f == src.prefix || strings.HasSuffix(src.prefix, "/") && strings.HasPrefix(f, src.prefix)) && !seen[src.prefix] {
				seen[src.prefix] = true
				_, _ = fmt.Fprintf(out, "    %s %s\n", ui.Bold(fmt.Sprintf("%-12s", src.prefix)), src.from)
			}
		}
	}
	if k := keepYAML(r.Keep, "    "); k != "" {
		_, _ = fmt.Fprintf(out, "\n  To keep what %s has, an edit made by hand say, add this to %s:\n\n", filepath.Base(s.Out), ui.Name(ConfigName))
		_ = ui.Highlight(out, "yaml", k)
	}
	_, _ = fmt.Fprintf(out, "\n  Then, or to write %s's change as it is:\n    %s\n\n",
		ConfigName, ui.Cmd("infrapi-cli dev up --force")+"   (or "+ui.Cmd("infrapi-cli dev render --force")+")")
}

// rendered checks the stack's files are rendered, for a command that runs
// on them as they are (ps, logs, down, token...): it warns when they differ
// from dev.yaml, and fails when there are none.
func (s *Stack) rendered() error {
	r, err := Render(s.Config, s.Out, RenderOptions{})
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(s.Out, "env")); err != nil {
		return fmt.Errorf("%s is not rendered: infrapi-cli dev up --force", s.Out)
	}
	if r.Pending {
		_, _ = fmt.Fprintf(ui.Writer(s.stderr), "%s %s differs from %s (%s): %s shows the diff, %s writes it\n",
			ui.Warn("warning:"), filepath.Base(s.Out), ConfigName, strings.Join(r.Files, ", "),
			ui.Cmd("infrapi-cli dev render"), ui.Cmd("infrapi-cli dev up --force"))
	}
	return nil
}

// Build builds localImage from kayra.src; it says so and does nothing
// without one.
func (s *Stack) Build(ctx context.Context) error {
	src := s.Config.Kayra.Src
	if src == "" {
		_, _ = fmt.Fprintf(ui.Writer(s.stdout), "build: kayra.src is not set in %s: the stack runs %s, %s\n", ConfigName, ui.Name(s.Config.Kayra.Image), ui.Warn("nothing to build"))
		return nil
	}
	if !filepath.IsAbs(src) {
		src = filepath.Join(s.Dir, src)
	}
	if _, err := os.Stat(filepath.Join(src, "go.mod")); err != nil {
		return fmt.Errorf("build: no Kayra checkout at %s (kayra.src in %s)", src, ConfigName)
	}
	args := append([]string{}, s.build...)
	// the build downloads Go modules: the proxy, by name only, so the
	// command printed shows no password; the engine reads the value from
	// its environment
	for _, kv := range netproxy.Set() {
		args = append(args, "--build-arg", kv[0])
	}
	args = append(args, "--build-context", "src="+src, "-t", localImage, filepath.Join(filepath.Base(s.Out), "kayra"))
	return s.run(ctx, Cmd{Args: args})
}

// Up renders, builds when kayra.src is set, and starts the stack. Kayra, its
// reconciler and gateway-init are recreated when what they read changed
// since they started: compose recreates a service whose image, ports or
// environment changed, not one whose mounted file did.
func (s *Stack) Up(ctx context.Context, o RenderOptions) error {
	r, err := s.Render(o)
	if err != nil {
		return err
	}
	return s.start(ctx, r, false)
}

// kayraServices are the services that run Kayra's image: kayra-dev:local,
// built, when kayra.src is set.
var kayraServices = []string{"gateway-init", "kayra", "reconciler"}

// Update renders, pulls the stack's images again, a tag like latest moved
// say, and starts the stack, every container recreated on its image as
// pulled. With kayra.src set, Kayra's image is built instead: the other
// services are pulled, and Kayra rebuilt.
func (s *Stack) Update(ctx context.Context, o RenderOptions) error {
	r, err := s.Render(o)
	if err != nil {
		return err
	}
	pull := []string{"pull"}
	if s.Config.Kayra.Src != "" {
		// compose.config and compose.override.yaml may add services
		services, err := s.output(ctx, Cmd{Args: s.composeArgs("config", "--services"), Env: s.composeEnv()})
		if err != nil {
			return err
		}
		for _, sv := range strings.Fields(services) {
			if !slices.Contains(kayraServices, sv) {
				pull = append(pull, sv)
			}
		}
	}
	if len(pull) > 1 || s.Config.Kayra.Src == "" {
		if err := s.run(ctx, Cmd{Args: s.composeArgs(pull...), Env: s.composeEnv()}); err != nil {
			return err
		}
	}
	return s.start(ctx, r, true)
}

// start builds when kayra.src is set and starts the stack: every container
// recreated with all, else Kayra's three when what they read changed.
func (s *Stack) start(ctx context.Context, r Rendered, all bool) error {
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
	up := []string{"up", "-d"}
	if all {
		up = append(up, "--force-recreate")
	}
	if err := s.run(ctx, Cmd{Args: s.composeArgs(up...), Env: s.composeEnv()}); err != nil {
		return err
	}
	if running != "" && r.Kayra && !all {
		_, _ = fmt.Fprintf(ui.Writer(s.stdout), "up: %s changed: %s\n", ConfigName, ui.Warn("recreating kayra, reconciler, gateway-init and dex"))
		recreate := []string{"up", "-d", "--force-recreate", "--no-deps", "kayra", "reconciler", "gateway-init"}
		if s.Config.Login() {
			recreate = append(recreate, "dex") // its users, dex/config.yaml
		}
		if err := s.run(ctx, Cmd{Args: s.composeArgs(recreate...), Env: s.composeEnv()}); err != nil {
			return err
		}
	}
	p := s.Config.Ports
	// the pages worth opening: a click on each shows something
	url := func(port int, path string) string { return fmt.Sprintf("http://localhost:%d%s", port, path) }
	links := [][2]string{
		{"Kayra", url(p.Kayra, wellKnown)},
		{"gateway", url(p.Gateway, wellKnown)},
		{"APISIX Admin API", url(p.Admin, "/ui/")},
	}
	if s.Config.Login() {
		links = append(links, [2]string{"Dex", s.Config.DexIssuer() + "/.well-known/openid-configuration"})
	}
	out := ui.Writer(s.stdout)
	_, _ = fmt.Fprintln(out)
	for _, l := range links {
		// padded before styling: the escapes would count
		_, _ = fmt.Fprintf(out, "  %s %s\n", ui.Bold(fmt.Sprintf("%-17s", l[0])), ui.URL(l[1]))
	}
	_, _ = fmt.Fprintf(out, "\n  %s %s\n  %s %s\n", ui.Bold(fmt.Sprintf("%-17s", "next:")), ui.Cmd("infrapi-cli dev contexts"),
		ui.Bold(fmt.Sprintf("%-17s", "or:")), ui.Cmd(fmt.Sprintf("infrapi-cli auth login --server http://localhost:%d", p.Gateway)))
	if s.Config.Login() && len(s.Config.Users) > 0 {
		s.printAccounts()
	}
	return nil
}

// printAccounts lists the accounts to log in with at the stack's Dex:
// dev.yaml's users, with their passwords, the stack's only.
func (s *Stack) printAccounts() {
	out := ui.Writer(s.stdout)
	_, _ = fmt.Fprintf(out, "\n  %s, to log in at Dex:\n\n", ui.Bold("accounts"))
	rows := make([][]string, 0, len(s.Config.Users))
	for _, u := range s.Config.Users {
		groups := strings.Join(u.Groups, ", ")
		if groups == "" {
			groups = "-"
		}
		rows = append(rows, []string{"  " + u.email(), u.password(), groups})
	}
	_ = ui.Table(out, []string{"  E-MAIL", "PASSWORD", "GROUPS"}, rows, nil)
	_, _ = fmt.Fprintln(out)
}

// wellKnown is Kayra's discovery document, the first page to open.
const wellKnown = "/.well-known/kayra.json"

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
	if err := s.rendered(); err != nil {
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
				_, _ = fmt.Fprintf(ui.Writer(s.stdout), "  %s %s %s\n", ui.OK("ok      "), ui.Bold(fmt.Sprintf("%-8s", check.name)), check.url)
				continue
			}
		}
		_, _ = fmt.Fprintf(ui.Writer(s.stdout), "  %s %s %s: %s\n", ui.Fail("FAILING "), ui.Bold(fmt.Sprintf("%-8s", check.name)), check.url, status)
		failed = append(failed, check.name)
	}
	if len(failed) > 0 {
		return fmt.Errorf("health: %s not answering: infrapi-cli dev ps, dev logs", strings.Join(failed, ", "))
	}
	return nil
}

// Routes puts the stack's own routes back into the gateway, dev.yaml's
// gateway.routes merged in, and deletes the ones it removes.
func (s *Stack) Routes(ctx context.Context) error {
	routes, removed, err := stackRoutes(s.Config)
	if err != nil {
		return err
	}
	g := s.Gateway()
	out := ui.Writer(s.stdout)
	for _, r := range routes {
		if err := g.PutRoute(ctx, r.ID, r.Body); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "routes: %s\n", ui.Name(r.ID))
	}
	for _, id := range removed {
		if err := g.DeleteRoute(ctx, id); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "routes: %s %s\n", ui.Name(id), ui.OK("removed"))
	}
	return nil
}

// Gateway is the stack's APISIX Admin API.
func (s *Stack) Gateway() *Gateway {
	return &Gateway{URL: fmt.Sprintf("http://localhost:%d", s.Config.Ports.Admin), Key: AdminKey, HTTP: s.http}
}

// Check lints the stack's script.
func (s *Stack) Check(ctx context.Context) error {
	return s.run(ctx, Cmd{Args: []string{"sh", "-n", filepath.Join(filepath.Base(s.Out), "apisix", "routes.sh")}})
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
// API.
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
		checks = append(checks, Check{Name: "go", Optional: true, Detail: "to build and run your API: https://go.dev/dl/"})
	}
	// infrapi-cli, go, podman's pulls and the image build take it; docker's
	// pulls take the daemon's own
	var set []string
	for _, kv := range netproxy.Set() {
		set = append(set, kv[0]+"="+kv[1])
	}
	if len(set) > 0 {
		checks = append(checks, Check{Name: "proxy", OK: true, Optional: true, Detail: strings.Join(set, " ")})
	} else {
		checks = append(checks, Check{Name: "proxy", OK: true, Optional: true, Detail: "none: https_proxy, http_proxy, no_proxy unset"})
	}
	return checks
}
