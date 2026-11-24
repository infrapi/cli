package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/urfave/cli/v3"

	"github.com/infrapi/cli/internal/app"
	"github.com/infrapi/cli/internal/dev"
	"github.com/infrapi/cli/internal/ui"
)

// appCommand is an API project's life: init writes it once, add a
// resource to its contract, generate its Go code from its contracts,
// register it in Kayra.
func appCommand() *cli.Command {
	return &cli.Command{
		Name:  "app",
		Usage: "an API project on infrapi/lib: create it, add resources to its contract, generate its code, run it, register it in Kayra",
		Commands: []*cli.Command{
			{
				Name:      "init",
				Usage:     "create an API project: a Go module, its contract, its CI and release, its dev.yaml",
				ArgsUsage: "[name]",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "module", Usage: "its Go module (default: example.com/<name>)"},
					&cli.StringFlag{Name: "group", Usage: "its API group, its paths' /apis/<group>/v1 (default: <name>.infrapi.io)"},
					&cli.StringFlag{Name: "ci", Usage: "github, gitlab or none (default: github)"},
					&cli.StringSliceFlag{Name: "package", Usage: "what a release builds: archive, deb, rpm, image, or none (repeatable; default: every one)"},
					&cli.StringFlag{Name: "image", Usage: "the image's name, without a tag (default: from the module, ghcr.io/<owner>/<name> for github.com)"},
					&cli.StringFlag{Name: "goprivate", Usage: "GOPRIVATE for the CI and the Makefile, the private modules: gitlab.example.com/*"},
					&cli.StringFlag{Name: "dir", Value: ".", Usage: "where the project's directory is created"},
					&cli.BoolFlag{Name: "no-prompt", Usage: "ask nothing: what no flag gives takes its default, and [name] is required"},
				},
				Action: runAppInit,
			},
			{
				Name:      "add",
				Usage:     "add a resource to the contract: its paths and schema, then its Go code, its operations answering 501 until written",
				ArgsUsage: "<resource>",
				Description: "<resource> is singular or plural, vm or vms: the other form is inferred, an English rule of thumb (policy, policies). " +
					"When it gets one wrong, set it: infrapi-cli app add cache --plural caches, infrapi-cli app add --singular person --plural people.",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "singular", Usage: "its name, singular: environment (default: from <resource>)"},
					&cli.StringFlag{Name: "plural", Usage: "its paths' name, plural: environments (default: from <resource>)"},
					&cli.StringFlag{Name: "kind", Usage: "its schema's, its Go type's name (default: from the singular, VM for vm)"},
					&cli.BoolFlag{Name: "cluster", Usage: "outside the namespaces: /apis/<group>/<version>/<plural>"},
					&cli.StringFlag{Name: "version", Usage: "the contract's version, with more than one"},
				},
				Action: runAppAdd,
			},
			{
				Name:   "generate",
				Usage:  "write the Go code from the contracts, api/*.yaml: api/zz_generated.go, internal/<name>/zz_generated.go",
				Action: runAppGenerate,
			},
			{
				Name:   "run",
				Usage:  "run the API against the dev stack, with .infrapi-dev/app.env: go run ./cmd/<name>",
				Flags:  []cli.Flag{devConfigFlag()},
				Action: runAppRun,
			},
			{
				Name:  "register",
				Usage: "register the API in Kayra, or register it again: Kayra reads its spec again; with dev.yaml, route the dev stack's gateway to it first",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "owner", Value: "dev", Usage: "the registration's owner"},
					&cli.StringFlag{Name: "upstream", Usage: "the gateway's upstream, host:port (default: dev.yaml's app.port on the host)"},
					&cli.BoolFlag{Name: "no-route", Usage: "register only: leave the gateway's routes alone"},
					&cli.StringFlag{Name: "jwks", Usage: "pin the API's identity keys: a JWK set file, or a PEM key whose public half is sent (default: the keys the API serves at -/jwks.json)"},
					&cli.BoolFlag{Name: "no-identity", Usage: "register the API without an identity: it gets no token"},
					devConfigFlag(),
				},
				Action: runAppRegister,
			},
			{
				Name:      "keygen",
				Usage:     "write a new identity key for the API, EC P-256, and print its public half: INFRAPI_IDENTITY_KEY_PATH",
				ArgsUsage: "<file>",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "force", Usage: "replace the file"},
				},
				Action: runAppKeygen,
			},
		},
	}
}

func runAppInit(ctx context.Context, cmd *cli.Command) error {
	o := app.Options{
		Name: cmd.Args().First(), Dir: cmd.String("dir"), Module: cmd.String("module"), Group: cmd.String("group"),
		CI: cmd.String("ci"), Packages: cmd.StringSlice("package"), Image: cmd.String("image"), GoPrivate: cmd.String("goprivate"),
	}
	if cmd.Bool("no-prompt") || !interactive() {
		if o.Name == "" {
			return errors.New("app init: a name is required without prompts")
		}
	} else if err := app.Ask(&o); err != nil {
		return err
	}
	root, err := app.Init(o)
	if err != nil {
		return err
	}
	out := ui.Stdout()
	_, _ = fmt.Fprintf(out, "%s %s\n", ui.Name(root), ui.OK("created"))
	// go.sum: the project builds as it is
	if _, err := exec.LookPath("go"); err == nil {
		tidy := exec.CommandContext(ctx, "go", "mod", "tidy") //#nosec G204 -- go itself
		tidy.Dir, tidy.Stdout, tidy.Stderr = root, os.Stdout, os.Stderr
		tidy.Env = append(os.Environ(), "GOWORK=off")
		if o.GoPrivate != "" {
			tidy.Env = append(tidy.Env, "GOPRIVATE="+o.GoPrivate)
		}
		_, _ = fmt.Fprintf(out, "%s\n", ui.Faint("+ go mod tidy"))
		if err := tidy.Run(); err != nil {
			_, _ = fmt.Fprintf(ui.Stderr(), "%s go mod tidy: %v: run it in %s\n", ui.Warn("warning:"), err, root)
		}
	} else {
		_, _ = fmt.Fprintf(ui.Stderr(), "%s go is not installed: run go mod tidy in %s\n", ui.Warn("warning:"), root)
	}
	_, _ = fmt.Fprintf(out, "\n  next:\n\n")
	for _, c := range []string{"cd " + root, "$EDITOR dev.yaml", "infrapi-cli dev init", "infrapi-cli dev up",
		"infrapi-cli dev contexts", "infrapi-cli app add <resource>"} {
		_, _ = fmt.Fprintf(out, "    %s\n", ui.Cmd(c))
	}
	_, _ = fmt.Fprintln(out)
	return nil
}

func runAppAdd(_ context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() > 1 || (cmd.Args().Len() == 0 && cmd.String("singular") == "" && cmd.String("plural") == "") {
		return errors.New("app add: one resource, singular or plural: infrapi-cli app add vm")
	}
	root, err := app.FindRoot(".")
	if err != nil {
		return err
	}
	a, err := app.Add(root, app.Resource{
		Name: cmd.Args().First(), Singular: cmd.String("singular"), Plural: cmd.String("plural"), Kind: cmd.String("kind"),
		Cluster: cmd.Bool("cluster"), Version: cmd.String("version"),
	})
	out := ui.Stdout()
	if a.Contract != "" {
		_, _ = fmt.Fprintf(out, "%s %s, %s %s\n", ui.Bold("singular"), ui.Name(a.Singular), ui.Bold("plural"), ui.Name(a.Plural))
		_, _ = fmt.Fprintf(out, "%s %s\n", ui.Name(a.Contract), ui.OK("updated"))
	}
	for _, f := range a.Files {
		_, _ = fmt.Fprintf(out, "%s %s\n", ui.Name(f), ui.OK("written"))
	}
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "\n  %s: %s, each answering 501 until written.\n  %s: its fields in %s, then %s.\n\n",
		ui.Bold("operations"), strings.Join(a.Ops, ", "), ui.Bold("next"), ui.Name(a.Contract), ui.Cmd("infrapi-cli app generate"))
	return nil
}

// devConfigFlag is the dev stack an app command works with, as the dev
// commands read it: --config, or DEV_CONFIG (dev.local.yaml, say).
func devConfigFlag() cli.Flag {
	return &cli.StringFlag{Name: "config", Sources: cli.EnvVars("DEV_CONFIG"), Usage: "the dev stack's dev.yaml (default: the project's)"}
}

// projectStack is the dev stack the project runs against: --config's or
// DEV_CONFIG's dev.yaml, or the project's own; nil, with no error, when
// the project has none and none is given.
func projectStack(cmd *cli.Command, root string) (*dev.Stack, error) {
	path := cmd.String("config")
	if path == "" {
		path = filepath.Join(root, dev.ConfigName)
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
	}
	return dev.OpenStack(dev.StackOptions{Config: path})
}

func runAppRun(ctx context.Context, cmd *cli.Command) error {
	root, err := app.FindRoot(".")
	if err != nil {
		return err
	}
	// what it runs with is dev.yaml's (DEV_CONFIG's): a stale
	// .infrapi-dev/ is refused
	envFile := ""
	s, err := projectStack(cmd, root)
	if err != nil {
		return err
	}
	if s != nil {
		if _, err := s.Render(dev.RenderOptions{}); err != nil {
			return fmt.Errorf("app run: %w: infrapi-cli dev up --force applies %s", err, filepath.Base(s.ConfigPath))
		}
		envFile = filepath.Join(s.Out, "app.env")
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx, root, envFile, os.Stdout)
}

func runAppGenerate(_ context.Context, _ *cli.Command) error {
	root, err := app.FindRoot(".")
	if err != nil {
		return err
	}
	g, err := app.Generate(root)
	if err != nil {
		return err
	}
	out := ui.Stdout()
	if len(g.Files) == 0 {
		_, _ = fmt.Fprintf(out, "generate: %s\n", ui.OK("up to date"))
	}
	for _, f := range g.Files {
		_, _ = fmt.Fprintf(out, "%s %s\n", ui.Name(f), ui.OK("written"))
	}
	return nil
}

// routeGateway routes the dev stack's gateway to the API, when there is a
// stack (--config, DEV_CONFIG, or the project's dev.yaml).
func routeGateway(ctx context.Context, cmd *cli.Command, root, group string, out io.Writer) error {
	s, err := projectStack(cmd, root)
	if err != nil || s == nil {
		return err
	}
	upstream := cmd.String("upstream")
	if upstream == "" {
		upstream = fmt.Sprintf("%s:%d", s.HostAlias, s.Config.App.Port)
	}
	if err := s.Gateway().AddAPI(ctx, group, upstream); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "gateway: %s -> %s\n", ui.Name("/apis/"+group+"/*"), ui.Name(upstream))
	return nil
}

func runAppRegister(ctx context.Context, cmd *cli.Command) error {
	root, err := app.FindRoot(".")
	if err != nil {
		return err
	}
	info, err := app.Describe(root)
	if err != nil {
		return err
	}
	out := ui.Stdout()

	// the gateway first: the reconciler fetches the API's spec through it
	if !cmd.Bool("no-route") {
		if err := routeGateway(ctx, cmd, root, info.Group, out); err != nil {
			return err
		}
	}

	c, err := resolveContext(cmd)
	if err != nil {
		return err
	}
	doc, err := fetchKayra(ctx, c)
	if err != nil {
		return err
	}
	catalog := doc.builtinGroup("catalog")
	if catalog == "" {
		return fmt.Errorf("%s names no catalog group", c.Server)
	}
	base := "/apis/" + catalog + "/v1/apiregistrations"
	reg := map[string]any{
		"name": info.Group, "short_name": info.Name, "version": info.Versions[len(info.Versions)-1],
		"active": true, "owner": cmd.String("owner"), "description": info.Description,
	}
	identity, err := registrationIdentity(cmd)
	if err != nil {
		return err
	}
	if identity != nil {
		reg["identity"] = identity
	}
	body, err := json.Marshal(reg)
	if err != nil {
		return err
	}
	// created or replaced, a new sync_id: the reconciler reads its spec again
	status, resp, err := doVerbRequest(ctx, c, http.MethodPost, base, body)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		renderProblem(os.Stderr, status, resp)
		return fmt.Errorf("app register: %s: %d", info.Group, status)
	}
	_, _ = fmt.Fprintf(out, "kayra: %s %s, its spec read again, on %s\n", ui.Name(info.Group), ui.OK("registered"), ui.URL(c.Server))
	_, _ = fmt.Fprintf(out, "  %s: subscribe a namespace, %s\n", ui.Bold("next"),
		ui.Cmd(fmt.Sprintf(`infrapi-cli create subscriptions -n <namespace> -d '{"api": "%s"}'`, info.Group)))
	return nil
}

// registrationIdentity is the identity app register sends: the keys the API
// serves at -/jwks.json, the ones --jwks pins (a JWK set, or a PEM key's
// public half), or none with --no-identity.
func registrationIdentity(cmd *cli.Command) (map[string]any, error) {
	file := cmd.String("jwks")
	switch {
	case cmd.Bool("no-identity") && file != "":
		return nil, errors.New("app register: --jwks or --no-identity, not both")
	case cmd.Bool("no-identity"):
		return nil, nil
	case file == "":
		return map[string]any{"jwks_from": "spec"}, nil
	}
	data, err := os.ReadFile(file) //#nosec G304 -- the file the user names
	if err != nil {
		return nil, fmt.Errorf("--jwks: %w", err)
	}
	var set json.RawMessage
	if json.Unmarshal(data, &set) == nil {
		return map[string]any{"jwks": set}, nil
	}
	pub, err := app.PublicKeys(file)
	if err != nil {
		return nil, fmt.Errorf("--jwks: a JWK set, or a PEM key: %w", err)
	}
	return map[string]any{"jwks": pub}, nil
}

func runAppKeygen(_ context.Context, cmd *cli.Command) error {
	path := cmd.Args().First()
	if path == "" {
		return errors.New("app keygen: the key's file, say identity.pem")
	}
	set, err := app.Keygen(path, cmd.Bool("force"))
	if err != nil {
		return fmt.Errorf("app keygen: %w", err)
	}
	pub, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return err
	}
	// the public half on stdout, for a file or app register --jwks
	_, _ = fmt.Fprintf(ui.Stderr(), "%s %s, kid %s: INFRAPI_IDENTITY_KEY_PATH=%s\n", ui.Name(path), ui.OK("written"), set.Keys[0].Kid, path)
	_, _ = fmt.Fprintln(os.Stdout, string(pub))
	return nil
}
