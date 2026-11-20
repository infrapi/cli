package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/infrapi/cli/internal/ui"

	"github.com/urfave/cli/v3"

	"github.com/infrapi/cli/internal/dev"
)

// devCommand is the local dev stack: create one (init), then run it. Every
// subcommand but init works on the stack whose dev.yaml is in the working
// directory or above, or the one --config names.
func devCommand() *cli.Command {
	return &cli.Command{
		Name:  "dev",
		Usage: "a local InfraPI to develop an API against: Kayra, etcd and APISIX on podman or docker",
		// inherited by every subcommand: infrapi-cli dev up --engine docker
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "config", Sources: cli.EnvVars("DEV_CONFIG"), Usage: "the stack's dev.yaml (default: dev.yaml here or in a parent directory)"},
			&cli.StringFlag{Name: "engine", Sources: cli.EnvVars("ENGINE"), Usage: "podman or docker (default: podman when installed, docker otherwise)"},
			&cli.StringFlag{Name: "compose", Sources: cli.EnvVars("COMPOSE"), Usage: `the compose command, "nerdctl compose" say; the stack's --env-file and -f are added (default: "<engine> compose")`},
			&cli.StringFlag{Name: "build-command", Sources: cli.EnvVars("BUILD"), Usage: `the image build command (default: "podman build --format docker" or "docker build")`},
			&cli.StringFlag{Name: "host-alias", Sources: cli.EnvVars("HOST_ALIAS"), Usage: "the host's name in the containers (default: host.containers.internal with podman, host.docker.internal with docker)"},
		},
		Commands: []*cli.Command{
			{
				Name:  "init",
				Usage: "create the dev stack beside dev.yaml, in .infrapi-dev/: write dev.yaml first when there is none, asking for its domain, platform, region and Kayra image",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "domain", Usage: "Kayra's API domain (default: infrapi.io)"},
					&cli.StringFlag{Name: "platform", Usage: "sandbox, preprod or prod (default: sandbox)"},
					&cli.StringFlag{Name: "region", Usage: "the region (default: eu-west-1)"},
					&cli.StringFlag{Name: "image", Usage: "Kayra's image (default: ghcr.io/infrapi/kayra:latest)"},
					&cli.BoolFlag{Name: "no-prompt", Usage: "ask nothing: what no flag gives takes its default"},
					forceFlag(),
				},
				Action: runDevInit,
			},
			{
				Name:   "doctor",
				Usage:  "check the prerequisites: podman or docker, its compose, go",
				Action: withStack(runDevDoctor),
			},
			{
				Name:   "render",
				Usage:  "show what dev.yaml changes in .infrapi-dev/, as a diff; --force writes it",
				Flags:  []cli.Flag{newKeysFlag(), forceFlag()},
				Action: withStack(runDevRender),
			},
			{
				Name:   "build",
				Usage:  "build kayra-dev:local from dev.yaml's kayra.src",
				Action: withStack(func(ctx context.Context, cmd *cli.Command, s *dev.Stack) error { return s.Build(ctx) }),
			},
			{
				Name:  "up",
				Usage: "render, build if kayra.src is set, start the stack; recreate Kayra when its configuration changed. A render that would change .infrapi-dev/ is canceled without --force",
				Flags: []cli.Flag{newKeysFlag(), forceFlag()},
				Action: withStack(func(ctx context.Context, cmd *cli.Command, s *dev.Stack) error {
					return s.Up(ctx, dev.RenderOptions{NewKeys: cmd.Bool("new-keys"), Force: cmd.Bool("force")})
				}),
			},
			{
				Name:  "update",
				Usage: "pull the stack's images again (Kayra's latest, say), then recreate its containers on them; with kayra.src, rebuild Kayra instead of pulling it",
				Flags: []cli.Flag{newKeysFlag(), forceFlag()},
				Action: withStack(func(ctx context.Context, cmd *cli.Command, s *dev.Stack) error {
					return s.Update(ctx, dev.RenderOptions{NewKeys: cmd.Bool("new-keys"), Force: cmd.Bool("force")})
				}),
			},
			composeCommand("down", "stop the stack, keep etcd's data", "down"),
			composeCommand("clean", "stop the stack and wipe etcd: Kayra's objects and APISIX's routes", "down", "-v"),
			{
				Name:   "reset-kayra",
				Usage:  "wipe Kayra's objects only (namespaces, APIs, RBAC...), keep the routes and the keys",
				Action: withStack(func(ctx context.Context, cmd *cli.Command, s *dev.Stack) error { return s.ResetKayra(ctx) }),
			},
			composeServicesCommand("restart", "restart services, or every one", "restart"),
			composeCommand("ps", "list the stack's containers", "ps"),
			composeServicesCommand("logs", "follow the logs of services (kayra, reconciler, apisix...), or all", "logs", "-f"),
			{
				Name:   "health",
				Usage:  "check Kayra and the gateway answer",
				Action: withStack(func(ctx context.Context, cmd *cli.Command, s *dev.Stack) error { return s.Health(ctx) }),
			},
			{
				Name:   "routes",
				Usage:  "put the stack's own gateway routes back as they were",
				Action: withStack(func(ctx context.Context, cmd *cli.Command, s *dev.Stack) error { return s.Routes(ctx) }),
			},
			{
				Name:  "token",
				Usage: "print a token for a user of dev.yaml, from Kayra's local provider",
				Flags: []cli.Flag{&cli.StringFlag{Name: "subject", Value: "jdoe", Usage: "the user"}},
				Action: withStack(func(ctx context.Context, cmd *cli.Command, s *dev.Stack) error {
					t, err := s.Mint(ctx, "local", cmd.String("subject"))
					if err == nil {
						fmt.Println(t)
					}
					return err
				}),
			},
			{
				Name:   "contexts",
				Usage:  "save dev-admin and dev-<user> contexts, on the gateway, with tokens from Kayra's own keys",
				Action: withStack(runDevContexts),
			},
			{
				Name:  "api",
				Usage: "route an API through the stack's gateway",
				Commands: []*cli.Command{
					{
						Name:      "add",
						Usage:     "route /apis/<api>/* to an upstream: dev.yaml's app, on the host, by default",
						ArgsUsage: "[api [upstream]]",
						Action:    withStack(runDevAPI(true)),
					},
					{
						Name:      "rm",
						Usage:     "remove an API's route",
						ArgsUsage: "[api]",
						Action:    withStack(runDevAPI(false)),
					},
				},
			},
			{
				Name:   "check",
				Usage:  "lint the stack's script",
				Action: withStack(func(ctx context.Context, cmd *cli.Command, s *dev.Stack) error { return s.Check(ctx) }),
			},
		},
	}
}

func forceFlag() cli.Flag {
	return &cli.BoolFlag{Name: "force", Usage: "write the render's changes to .infrapi-dev/, dropping any edit made there by hand: without it, a render that would change a file writes nothing and shows the diff"}
}

func newKeysFlag() cli.Flag {
	return &cli.BoolFlag{Name: "new-keys", Usage: "generate new signing keys: every token minted before is refused"}
}

// withStack opens the stack from dev's flags, then runs fn.
func withStack(fn func(context.Context, *cli.Command, *dev.Stack) error) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		s, err := dev.OpenStack(dev.StackOptions{
			Config:    cmd.String("config"),
			Engine:    cmd.String("engine"),
			Compose:   cmd.String("compose"),
			Build:     cmd.String("build-command"),
			HostAlias: cmd.String("host-alias"),
		})
		if err != nil {
			return err
		}
		return fn(ctx, cmd, s)
	}
}

// composeCommand runs compose with args.
func composeCommand(name, usage string, args ...string) *cli.Command {
	return &cli.Command{
		Name: name, Usage: usage,
		Action: withStack(func(ctx context.Context, cmd *cli.Command, s *dev.Stack) error { return s.Compose(ctx, args...) }),
	}
}

// composeServicesCommand runs compose with args, then the services given.
func composeServicesCommand(name, usage string, args ...string) *cli.Command {
	return &cli.Command{
		Name: name, Usage: usage, ArgsUsage: "[service...]",
		Action: withStack(func(ctx context.Context, cmd *cli.Command, s *dev.Stack) error {
			return s.Compose(ctx, append(append([]string{}, args...), cmd.Args().Slice()...)...)
		}),
	}
}

func runDevInit(_ context.Context, cmd *cli.Command) error {
	out := ui.Stdout()
	path := cmd.String("config")
	if path == "" {
		path = dev.ConfigName
	}
	switch _, err := os.Stat(path); {
	case errors.Is(err, fs.ErrNotExist):
		a := dev.Answers{Domain: cmd.String("domain"), Platform: cmd.String("platform"), Region: cmd.String("region"), Image: cmd.String("image")}
		if !cmd.Bool("no-prompt") && interactive() {
			if err := dev.Ask(&a); err != nil {
				return err
			}
		}
		if err := dev.WriteConfig(path, a); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "%s %s\n", ui.Name(path), ui.OK("written"))
	case err != nil:
		return err
	default:
		_, _ = fmt.Fprintf(out, "%s: the stack's settings\n", ui.Name(path))
	}

	s, err := dev.OpenStack(dev.StackOptions{Config: path})
	if err != nil {
		return err
	}
	// a new stack: nothing to overwrite yet, so it asks nothing
	_, err = os.Stat(s.Out)
	fresh := errors.Is(err, fs.ErrNotExist)
	if _, err := s.Render(dev.RenderOptions{Force: fresh || cmd.Bool("force")}); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "\n  %s is %s Edit %s, then %s applies it.\n\n", ui.Name(s.Out), ui.OK("ready."), ui.Name(dev.ConfigName), ui.Cmd("dev up --force"))
	for _, c := range []string{"infrapi-cli dev doctor", "infrapi-cli dev up", "infrapi-cli dev health", "infrapi-cli dev contexts"} {
		_, _ = fmt.Fprintf(out, "    %s\n", ui.Cmd(c))
	}
	_, _ = fmt.Fprintln(out)
	return nil
}

// interactive is whether stdin is a terminal, to ask questions on.
func interactive() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func runDevDoctor(ctx context.Context, _ *cli.Command, s *dev.Stack) error {
	missing := 0
	out := ui.Stdout()
	for _, c := range s.Doctor(ctx) {
		state := ui.OK(fmt.Sprintf("%-8s", "ok"))
		switch {
		case !c.OK && c.Optional:
			state = ui.Warn(fmt.Sprintf("%-8s", "optional"))
		case !c.OK:
			state = ui.Fail(fmt.Sprintf("%-8s", "MISSING"))
			missing++
		}
		_, _ = fmt.Fprintf(out, "  %s %s %s\n", state, ui.Bold(fmt.Sprintf("%-8s", c.Name)), c.Detail)
	}
	_, _ = fmt.Fprintf(out, "\n  engine %s, the host seen from the containers as %s\n", ui.Name(s.Engine), ui.Name(s.HostAlias))
	if missing > 0 {
		return fmt.Errorf("doctor: %d prerequisite(s) missing", missing)
	}
	return nil
}

func runDevRender(_ context.Context, cmd *cli.Command, s *dev.Stack) error {
	r, err := s.Render(dev.RenderOptions{NewKeys: cmd.Bool("new-keys"), Force: cmd.Bool("force")})
	if err != nil {
		return err
	}
	out := ui.Stdout()
	if r.Diff == "" {
		_, _ = fmt.Fprintf(out, "render: %s %s\n", s.Out, ui.OK("up to date"))
	} else {
		_, _ = fmt.Fprintf(out, "render: %s %s\n", s.Out, ui.OK("written"))
	}
	image := s.Config.Kayra.Image
	if s.Config.Kayra.Src != "" {
		image = "built from " + s.Config.Kayra.Src
	}
	_, _ = fmt.Fprintf(out, "render: %s, domain %s, kayra %s\n", s.Out, ui.Name(s.Config.Domain), ui.Name(image))
	return nil
}

func runDevContexts(ctx context.Context, _ *cli.Command, s *dev.Stack) error {
	names, err := s.Contexts(ctx)
	if err != nil {
		return err
	}
	out := ui.Stdout()
	styled := make([]string, len(names))
	for i, n := range names {
		styled[i] = ui.Name(n)
	}
	_, _ = fmt.Fprintf(out, "contexts: %s %s, on %s; %s is the current one\n", strings.Join(styled, ", "), ui.OK("saved"),
		ui.URL(fmt.Sprintf("http://localhost:%d", s.Config.Ports.Gateway)), ui.Name("dev-admin"))
	_, _ = fmt.Fprintf(out, "  %s %s, then develop an API: %s\n", ui.Bold("next:"), ui.Cmd("infrapi-cli app add <resource>"),
		ui.URL("https://infrapi.github.io/products/cli/docs/develop/develop-an-api/"))
	return nil
}

func runDevAPI(add bool) func(context.Context, *cli.Command, *dev.Stack) error {
	return func(ctx context.Context, cmd *cli.Command, s *dev.Stack) error {
		c := s.Config
		api, upstream := c.FullAPI(c.App.API), fmt.Sprintf("%s:%d", s.HostAlias, c.App.Port)
		if cmd.Args().Len() > 0 {
			api = c.FullAPI(cmd.Args().Get(0))
		}
		if cmd.Args().Len() > 1 {
			upstream = cmd.Args().Get(1)
		}
		g := s.Gateway()
		if !add {
			if err := g.RemoveAPI(ctx, api); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(ui.Stdout(), "api: %s %s\n", ui.Name(api), ui.OK("removed"))
			return nil
		}
		if err := g.AddAPI(ctx, api, upstream); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(ui.Stdout(), "api: %s -> %s\n", ui.Name("/apis/"+api+"/*"), ui.Name(upstream))
		return nil
	}
}
