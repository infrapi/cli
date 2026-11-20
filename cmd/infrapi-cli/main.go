// Command infrapi-cli is InfraPI's command line: the portable client of a
// Kayra server's own API (login, contexts, can-i, apis, and the get,
// create, update, patch and delete verbs), and the local dev stack (dev
// init, up, contexts, api...). Everything it does against Kayra goes
// through Kayra's API, never local server code or config: `kayra`, the
// server binary, ships with its kayra.yaml and backends; this one only
// needs a server URL.
package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/infrapi/cli/internal/clicontext"
	"github.com/infrapi/cli/internal/netproxy"
	"github.com/infrapi/cli/internal/oauth2login"
	"github.com/infrapi/cli/internal/ui"
)

// version is set at build time via -ldflags "-X main.version=..." —
// see .goreleaser.yaml. "dev" is what `go build`/`go run` without
// that flag leaves it at: buildVersion adds the commit.
var version = "dev"

func main() {
	// https_proxy, http_proxy, no_proxy: Kayra's API, the login, APISIX's
	// Admin API, dev init's template clone
	netproxy.Setup()
	if err := newRoot().Run(context.Background(), os.Args); err != nil {
		_, _ = fmt.Fprintln(ui.Stderr(), ui.Fail("infrapi-cli:"), err)
		os.Exit(1)
	}
}

// newRoot is infrapi-cli's command tree.
func newRoot() *cli.Command {
	return &cli.Command{
		Name:    "infrapi-cli",
		Usage:   "InfraPI's command line: log in to Kayra and call its API, and run a local dev stack",
		Version: buildVersion(),
		// inherited by every command, before or after it: a context for this
		// one call, the saved current one left as it is
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "context",
				Sources: cli.EnvVars("INFRAPI_CONTEXT"),
				Usage:   "saved context to use for this command, without changing the current one",
			},
			// NO_COLOR (https://no-color.org), any value but empty, is read
			// by internal/ui: a bool flag would refuse NO_COLOR=yes
			&cli.BoolFlag{
				Name:  "no-color",
				Usage: "no colors, even on a terminal; also NO_COLOR=1. Colors are off anyway when the output is not a terminal",
				Action: func(_ context.Context, _ *cli.Command, v bool) error {
					ui.SetNoColor(v)
					return nil
				},
			},
		},
		Commands: append([]*cli.Command{
			authCommand(),
			apisCommand(),
			appCommand(),
			devCommand(),
		}, verbCommands()...),
		CommandNotFound: func(ctx context.Context, cmd *cli.Command, name string) {
			_, _ = fmt.Fprintf(ui.Stderr(), "%s unknown command %q\n\n", ui.Fail("infrapi-cli:"), name)
			_ = cli.ShowAppHelp(cmd)
			os.Exit(2)
		},
	}
}

// contextNameSanitizer collapses anything that isn't a lowercase
// letter, digit, underscore or dot into a single '-' — keeps a
// server-derived context name readable (and shell/YAML-key-safe)
// without needing a real slug library for what's really just a host.
var contextNameSanitizer = regexp.MustCompile(`[^a-z0-9_.]+`)

// sanitizeContextName derives `auth login`'s default --context-name from
// --server: its host[:port] (dropping scheme and path — the part that
// actually tells two servers apart), lowercased and sanitized. Falls
// back to "default" for a URL with no host at all.
func sanitizeContextName(server string) string {
	name := server
	if u, err := url.Parse(server); err == nil && u.Host != "" {
		name = u.Host
	}
	name = contextNameSanitizer.ReplaceAllString(strings.ToLower(name), "-")
	name = strings.Trim(name, "-")
	if name == "" {
		return "default"
	}
	return name
}

// loginCommand logs in at --server's authorization server: the browser goes
// to the provider it sends it to, and the context keeps Kayra's tokens, the
// access token renewed before it expires (resolveContext). It never reads
// local config and never names a provider; see internal/oauth2login.
func loginCommand() *cli.Command {
	return &cli.Command{
		Name:  "login",
		Usage: "log in to a Kayra server, through the browser: its tokens kept, renewed before they expire",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "server", Required: true, Usage: "Kayra server base URL, e.g. https://kayra.example.com"},
			&cli.StringFlag{Name: "context-name", Usage: "name to save this login under (defaults to the sanitized --server host)"},
			&cli.DurationFlag{Name: "timeout", Value: 2 * time.Minute, Usage: "how long to wait for the browser round trip before giving up (default 10m with --no-browser)"},
			&cli.BoolFlag{Name: "no-browser", Usage: "for a terminal with no usable browser (SSH, container, air-gapped host): print the login URL to open on any other machine, then paste back the code it shows"},
		},
		Action: runLogin,
	}
}

func runLogin(ctx context.Context, cmd *cli.Command) error {
	server, err := serverURL(cmd.String("server"))
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	if cmd.Args().Len() > 0 {
		return fmt.Errorf("login: unexpected argument %q: login takes none, only flags", cmd.Args().First())
	}

	noBrowser := cmd.Bool("no-browser")
	timeout := cmd.Duration("timeout")
	if noBrowser && !cmd.IsSet("timeout") {
		// Logging in on another machine and copying a code back takes
		// longer than a local browser round trip; 10m is how long Kayra
		// keeps a login in flight.
		timeout = 10 * time.Minute
	}
	loginCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var result *oauth2login.Result
	if noBrowser {
		result, err = oauth2login.LoginToServerManual(loginCtx, server, os.Stdin, os.Stderr)
	} else {
		result, err = oauth2login.LoginToServer(loginCtx, server, os.Stderr)
	}
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}

	name := cmp.Or(cmd.String("context-name"), sanitizeContextName(server))
	f, err := clicontext.Load()
	if err != nil {
		return err
	}
	f.SetContext(name, clicontext.Context{
		Server:       server,
		Token:        result.AccessToken,
		RefreshToken: result.RefreshToken,
		TokenType:    result.TokenType,
		Expiry:       result.Expiry,
	})
	if err := f.Save(); err != nil {
		return err
	}

	path, err := clicontext.Path()
	if err != nil {
		return err
	}
	// The token itself is deliberately never printed here — it's
	// sitting in the context file this just wrote, retrievable any
	// time via `auth context token`, which is the one place it should have
	// to be read back from.
	_, _ = fmt.Fprintf(ui.Stderr(), "infrapi-cli: %s saved context %s to %s (now current)\n", ui.OK("logged in:"), ui.Name(name), path)
	_, _ = fmt.Fprintf(ui.Stdout(), "infrapi-cli: run %s to print the bearer token\n", ui.Cmd("infrapi-cli auth context token"))
	return nil
}

// contextCommand manages ~/.config/infrapi/config — the kubeconfig-style
// current-context/contexts file `auth login` writes to (see
// internal/clicontext), so a saved login can be listed, switched back
// to, or retrieved again without logging in a second time.
func contextCommand() *cli.Command {
	return &cli.Command{
		Name:    "context",
		Aliases: []string{"ctx"},
		Usage:   "manage saved logins in ~/.config/infrapi/config",
		Commands: []*cli.Command{
			{
				Name:   "list",
				Usage:  "list saved contexts, marking the current one",
				Action: runContextList,
			},
			{
				Name:   "current",
				Usage:  "show the current context's name and details",
				Action: runContextCurrent,
			},
			{
				Name:      "set",
				Usage:     "save a context from an existing bearer token (e.g. a `kayra token mint` bootstrap token) and make it current",
				ArgsUsage: "<name>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "server", Required: true, Usage: "Kayra server base URL, e.g. http://localhost:8080"},
					&cli.StringFlag{Name: "token", Required: true, Usage: "bearer token to save"},
				},
				Action: runContextSet,
			},
			{
				Name:      "use",
				Usage:     "switch the current context",
				ArgsUsage: "<name>",
				Action:    runContextUse,
			},
			{
				Name:      "delete",
				Usage:     "delete a saved context",
				ArgsUsage: "<name>",
				Action:    runContextDelete,
			},
			{
				Name:      "namespace",
				Aliases:   []string{"ns"},
				Usage:     "set the namespace commands address when they name none (-n), like kubens; print it without <name>",
				ArgsUsage: "[<name>]",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "unset", Usage: "forget the namespace: every namespaced command then needs -n"},
				},
				Action: runContextNamespace,
			},
			{
				Name:      "token",
				Usage:     "print a context's bearer token (defaults to the current context)",
				ArgsUsage: "[name]",
				Action:    runContextToken,
			},
		},
	}
}

func runContextList(ctx context.Context, cmd *cli.Command) error {
	f, err := clicontext.Load()
	if err != nil {
		return err
	}
	if len(f.Contexts) == 0 {
		_, _ = fmt.Fprintf(ui.Stdout(), "infrapi-cli: no saved contexts — run %s\n", ui.Cmd("infrapi-cli auth login --server <url>"))
		return nil
	}

	var rows [][]string
	names := f.Names()
	for _, name := range names {
		c := f.Contexts[name]
		current := ""
		if name == f.CurrentContext {
			current = "*"
		}
		expiry := "-"
		if !c.Expiry.IsZero() {
			expiry = c.Expiry.Local().Format(time.RFC3339)
		}
		rows = append(rows, []string{current, name, c.Provider, c.Server, cmp.Or(c.Namespace, "-"), expiry})
	}
	return ui.Table(os.Stdout, []string{"CURRENT", "NAME", "PROVIDER", "SERVER", "NAMESPACE", "EXPIRY"}, rows,
		func(i int) bool { return names[i] == f.CurrentContext })
}

func runContextCurrent(ctx context.Context, cmd *cli.Command) error {
	f, err := clicontext.Load()
	if err != nil {
		return err
	}
	name, c, err := f.Current()
	if err != nil {
		return err
	}

	pairs := [][2]string{{"name", ui.Name(name)}, {"provider", c.Provider}, {"server", c.Server}}
	if c.Username != "" {
		pairs = append(pairs, [2]string{"username", c.Username})
	}
	if c.Namespace != "" {
		pairs = append(pairs, [2]string{"namespace", c.Namespace})
	}
	if !c.Expiry.IsZero() {
		pairs = append(pairs, [2]string{"expiry", c.Expiry.Local().Format(time.RFC3339)})
	}
	return ui.KeyValues(os.Stdout, pairs)
}

// runContextSet saves --token as context <name> without any login
// flow — the way in for a bootstrap token minted by `kayra token mint`,
// before any real login provider can be used. It always becomes the
// current context, exactly like a login.
func runContextSet(ctx context.Context, cmd *cli.Command) error {
	name := cmd.Args().First()
	if name == "" || cmd.Args().Len() > 1 {
		return fmt.Errorf("auth context: usage: infrapi-cli auth context set <name> --server <url> --token <token>")
	}
	server, err := serverURL(cmd.String("server"))
	if err != nil {
		return fmt.Errorf("auth context: %w", err)
	}

	f, err := clicontext.Load()
	if err != nil {
		return err
	}
	f.SetContext(name, clicontext.Context{
		Server:    server,
		Token:     cmd.String("token"),
		TokenType: "Bearer",
	})
	if err := f.Save(); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(ui.Stdout(), "infrapi-cli: %s %s (now current)\n", ui.OK("saved context"), ui.Name(name))
	return nil
}

func runContextUse(ctx context.Context, cmd *cli.Command) error {
	name := cmd.Args().First()
	if name == "" {
		return fmt.Errorf("auth context: usage: infrapi-cli auth context use <name>")
	}

	f, err := clicontext.Load()
	if err != nil {
		return err
	}
	if err := f.Use(name); err != nil {
		return err
	}
	if err := f.Save(); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(ui.Stdout(), "infrapi-cli: %s %s\n", ui.OK("switched to context"), ui.Name(name))
	return nil
}

func runContextDelete(ctx context.Context, cmd *cli.Command) error {
	name := cmd.Args().First()
	if name == "" {
		return fmt.Errorf("auth context: usage: infrapi-cli auth context delete <name>")
	}

	f, err := clicontext.Load()
	if err != nil {
		return err
	}
	if err := f.Delete(name); err != nil {
		return err
	}
	if err := f.Save(); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(ui.Stdout(), "infrapi-cli: %s %s\n", ui.OK("deleted context"), ui.Name(name))
	return nil
}

// runContextNamespace sets, prints or forgets (--unset) the namespace of
// the current context, or of --context's: like kubens, but per context.
func runContextNamespace(ctx context.Context, cmd *cli.Command) error {
	f, err := clicontext.Load()
	if err != nil {
		return err
	}
	name := cmd.String("context")
	if name == "" {
		if name, _, err = f.Current(); err != nil {
			return err
		}
	}
	c, ok := f.Contexts[name]
	if !ok {
		return fmt.Errorf("auth context namespace: no such context %q", name)
	}
	ns := cmd.Args().First()
	switch {
	case cmd.Bool("unset"):
		ns = ""
	case ns == "":
		if c.Namespace == "" {
			return fmt.Errorf("auth context namespace: context %s has no namespace: set one with infrapi-cli auth context namespace <name>", name)
		}
		_, err := fmt.Fprintln(os.Stdout, c.Namespace)
		return err
	}
	if err := f.SetNamespace(name, ns); err != nil {
		return err
	}
	if err := f.Save(); err != nil {
		return err
	}
	if ns == "" {
		_, _ = fmt.Fprintf(ui.Stdout(), "infrapi-cli: context %s %s\n", ui.Name(name), ui.OK("has no namespace"))
		return nil
	}
	_, _ = fmt.Fprintf(ui.Stdout(), "infrapi-cli: context %s %s %s\n", ui.Name(name), ui.OK("now addresses namespace"), ui.Name(ns))
	return nil
}

// runContextToken prints a context's access token, renewed first when it
// is about to expire: what a script's curl sends.
func runContextToken(ctx context.Context, cmd *cli.Command) error {
	f, err := clicontext.Load()
	if err != nil {
		return err
	}
	name := cmd.Args().First()
	if name == "" {
		if name, _, err = f.Current(); err != nil {
			return err
		}
	}
	c, err := fresh(f, name)
	if err != nil {
		return err
	}
	fmt.Println(c.Token)
	return nil
}

// authCommand groups what is about the login itself: "login", "logout", "context"
// (the saved logins), "can-i" (an authorization decision) and "whoami"
// (the identity that decision would be made against).
func authCommand() *cli.Command {
	return &cli.Command{
		Name:  "auth",
		Usage: "log in, keep the logins (context, ctx), list and revoke them at the server (sessions), and ask it about the current one: can-i, whoami",
		Commands: []*cli.Command{
			loginCommand(),
			logoutCommand(),
			sessionsCommand(),
			contextCommand(),
			canICommand(),
			whoamiCommand(),
		},
	}
}

// resolveContext picks --context (or $INFRAPI_CONTEXT) by name, for this
// command only, or the current context when it is unset — the one piece every auth subcommand needs
// before it can call anything.
func resolveContext(cmd *cli.Command) (clicontext.Context, error) {
	f, err := clicontext.Load()
	if err != nil {
		return clicontext.Context{}, err
	}
	name := cmd.String("context")
	if name == "" {
		if name, _, err = f.Current(); err != nil {
			return clicontext.Context{}, err
		}
	}
	return fresh(f, name)
}

// renewBefore is how long before it expires a login's access token is
// renewed.
const renewBefore = 30 * time.Second

// fresh is context name, its access token renewed first when it expires
// within renewBefore and the login has a refresh token: the new tokens are
// saved. A login revoked or expired asks for a new auth login.
func fresh(f *clicontext.File, name string) (clicontext.Context, error) {
	c, ok := f.Contexts[name]
	if !ok {
		return clicontext.Context{}, fmt.Errorf("no such context %q", name)
	}
	if c.Token == "" {
		return clicontext.Context{}, fmt.Errorf("context %s is logged out: run %s", name,
			ui.Cmd("infrapi-cli auth login --server "+c.Server+" --context-name "+name))
	}
	if c.RefreshToken == "" || c.Expiry.IsZero() || time.Until(c.Expiry) > renewBefore {
		return c, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := oauth2login.Refresh(ctx, c.Server, c.RefreshToken)
	var oerr *oauth2login.Error
	if errors.As(err, &oerr) && oerr.Code == "invalid_grant" {
		return clicontext.Context{}, fmt.Errorf("context %s: the login is revoked or expired (%s): run %s",
			name, oerr.Description, ui.Cmd("infrapi-cli auth login --server "+c.Server+" --context-name "+name))
	}
	if err != nil {
		return clicontext.Context{}, fmt.Errorf("context %s: renew its token: %w", name, err)
	}
	c.Token, c.RefreshToken, c.Expiry = r.AccessToken, cmp.Or(r.RefreshToken, c.RefreshToken), r.Expiry
	f.Contexts[name] = c
	if err := f.Save(); err != nil {
		return clicontext.Context{}, err
	}
	return c, nil
}

// logoutCommand revokes the current context's login, or --context's, and
// forgets its tokens; the context stays, its server and namespace.
func logoutCommand() *cli.Command {
	return &cli.Command{
		Name:   "logout",
		Usage:  "revoke the current login at its server, and forget its tokens",
		Action: runLogout,
	}
}

func runLogout(ctx context.Context, cmd *cli.Command) error {
	f, err := clicontext.Load()
	if err != nil {
		return err
	}
	name := cmd.String("context")
	if name == "" {
		if name, _, err = f.Current(); err != nil {
			return err
		}
	}
	c, ok := f.Contexts[name]
	if !ok {
		return fmt.Errorf("logout: no such context %q", name)
	}
	token := cmp.Or(c.RefreshToken, c.Token) // a refresh token revokes the whole login
	if token != "" {
		if err := oauth2login.Revoke(ctx, c.Server, token); err != nil {
			return fmt.Errorf("logout: %w", err)
		}
	}
	c.Token, c.RefreshToken, c.Expiry = "", "", time.Time{}
	f.Contexts[name] = c
	if err := f.Save(); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(ui.Stdout(), "infrapi-cli: %s %s\n", ui.OK("logged out of"), ui.Name(name))
	return nil
}

// canICommand mirrors `kubectl auth can-i`: ask the server's own
// rbac.RegisterAuthorizeRoute check — the endpoint its
// /.well-known/kayra.json names — whether the current context's own
// already-authenticated identity (never asserted locally) is granted
// verb on resource, named as kubectl names one (see resolveResource).
//
// Kayra's /check answers every route as that route decides it: an app's
// resources in a namespace on the namespace's own RBAC, a namespace's
// management (Kayra's groups, roles, rolebindings, subscriptions) on the
// cluster bindings scoped to it, and a cluster-scoped resource (no
// --namespace) on the cluster RBAC, the sysadmin binding included.
func canICommand() *cli.Command {
	return &cli.Command{
		Name:        "can-i",
		Usage:       "check whether the current login can do <verb> on <resource>",
		Description: "Asks Kayra's check endpoint, which answers as the real route would: an app's resources in a namespace, a namespace's own groups, roles, rolebindings and subscriptions, or a cluster-scoped resource (namespaces, apiregistrations, clusterroles, an app's cluster-scoped resources). The resource is named as kubectl names one: vms, or vms.compute.infrapi.io.",
		ArgsUsage:   "<verb> <resource>[.<api group>][/<sub-resource>]",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "namespace", Aliases: []string{"n"}, Usage: "the namespace of a namespaced resource (default: the context's, see `auth context namespace`); a cluster-scoped one has none"},
		},
		Action: runCanI,
	}
}

func runCanI(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	if args.Len() != 2 {
		return fmt.Errorf("can-i: usage: infrapi-cli auth can-i <verb> <resource>[.<api group>] [-n <namespace>]")
	}
	verb, resource := args.Get(0), args.Get(1)

	c, err := resolveContext(cmd)
	if err != nil {
		return fmt.Errorf("can-i: %w", err)
	}

	doc, err := fetchKayra(ctx, c)
	if err != nil {
		return fmt.Errorf("can-i: %w", err)
	}
	ref, err := resolveResource(ctx, c, resource)
	if err != nil {
		warnAmbiguous(err)
		return fmt.Errorf("can-i: %w", err)
	}
	namespace, err := namespaceFor(ref, cmd.String("namespace"), c)
	if err != nil {
		return fmt.Errorf("can-i: %w", err)
	}

	allowed, reason, err := checkAuthorized(ctx, c, doc.Endpoints.Check, namespace, ref.Api, ref.Resource, verb)
	if err != nil {
		return fmt.Errorf("can-i: %w", err)
	}

	if allowed {
		_, _ = fmt.Fprintln(ui.Stdout(), ui.OK("yes"))
		return nil
	}
	_, _ = fmt.Fprintln(ui.Stdout(), ui.Fail("no"))
	if reason != "" {
		_, _ = fmt.Fprintln(ui.Stderr(), "infrapi-cli:", reason)
	}
	os.Exit(1)
	return nil
}

// whoamiCommand prints exactly what the server's own
// GET /oauth2/userinfo returns for the current login: sub, provider,
// authorization, groups, and whatever authorizations.<name> attributes
// (email, name, ...) that backend resolved — the same identity any
// can-i decision or RBAC check against this token is actually made
// against, never asserted locally.
func whoamiCommand() *cli.Command {
	return &cli.Command{
		Name:   "whoami",
		Usage:  "print the current login's identity and attributes, per the server's own userinfo",
		Flags:  []cli.Flag{},
		Action: runWhoami,
	}
}

func runWhoami(ctx context.Context, cmd *cli.Command) error {
	c, err := resolveContext(cmd)
	if err != nil {
		return fmt.Errorf("whoami: %w", err)
	}

	userinfoURL := strings.TrimRight(c.Server, "/") + "/oauth2/userinfo"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userinfoURL, nil)
	if err != nil {
		return fmt.Errorf("whoami: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("whoami: call %s: %w", userinfoURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("whoami: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("whoami: %s returned %d: %s", userinfoURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var attrs map[string]any
	if err := json.Unmarshal(body, &attrs); err != nil {
		return fmt.Errorf("whoami: parse response: %w", err)
	}

	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)

	pairs := make([][2]string, 0, len(names))
	for _, name := range names {
		pairs = append(pairs, [2]string{name, formatUserinfoValue(attrs[name])})
	}
	return ui.KeyValues(os.Stdout, pairs)
}

// formatUserinfoValue renders one userinfo value for display: groups
// (and any other array-valued attribute) as a comma-separated list,
// everything else via its natural string form.
func formatUserinfoValue(v any) string {
	list, ok := v.([]any)
	if !ok {
		return fmt.Sprint(v)
	}
	if len(list) == 0 {
		return "-"
	}
	parts := make([]string, len(list))
	for i, item := range list {
		parts[i] = fmt.Sprint(item)
	}
	return strings.Join(parts, ", ")
}

// checkAuthorized POSTs c's own bearer token against the server's check
// endpoint, checkPath (from its discovery document) — the exact same
// rbac.AuthorizeRequest/AuthorizeResponse shape infrapi/lib's own auth
// client uses, so this never duplicates or re-derives the decision
// itself, only asks for it.
func checkAuthorized(ctx context.Context, c clicontext.Context, checkPath, namespace, api, resource, verb string) (allowed bool, reason string, err error) {
	body, err := json.Marshal(map[string]string{
		"namespace": namespace,
		"api":       api,
		"resource":  resource,
		"verb":      verb,
	})
	if err != nil {
		return false, "", fmt.Errorf("encode request: %w", err)
	}

	checkURL := strings.TrimRight(c.Server, "/") + checkPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, checkURL, bytes.NewReader(body))
	if err != nil {
		return false, "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("call %s: %w", checkURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var problem struct {
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal(respBody, &problem)
		if problem.Detail != "" {
			return false, "", fmt.Errorf("%s returned %d: %s", checkURL, resp.StatusCode, problem.Detail)
		}
		return false, "", fmt.Errorf("%s returned %d", checkURL, resp.StatusCode)
	}

	var result struct {
		Allowed bool   `json:"allowed"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return false, "", fmt.Errorf("parse response: %w", err)
	}
	return result.Allowed, result.Reason, nil
}

// serverURL checks --server is a server's base URL, http(s) with a host:
// "--server --server https://..." takes the second --server for the
// first's value, which no browser could open.
func serverURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		if strings.HasPrefix(raw, "-") {
			return "", fmt.Errorf("--server %q is not a server's URL: a flag given twice? give --server once, then its URL", raw)
		}
		return "", fmt.Errorf("--server %q is not a server's URL: give one such as https://kayra.example.com", raw)
	}
	return strings.TrimRight(raw, "/"), nil
}
