package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/infrapi/cli/internal/ui"

	"github.com/urfave/cli/v3"

	"github.com/infrapi/cli/internal/dev"
)

// devCommand is the local dev stack: scaffold one (init), then run it. Every
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
				Name:      "init",
				Usage:     "scaffold a new dev stack from the infrapi/dev template, asking for its domain, platform, region and Kayra image",
				ArgsUsage: "[name]",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "template", Value: dev.DefaultTemplate, Usage: "the scaffold: a git URL (…@v1.2.0 for a tag, …#subdir for a directory) or a local path"},
					&cli.StringFlag{Name: "dir", Value: ".", Usage: "where the stack's directory is created"},
					&cli.StringSliceFlag{Name: "set", Usage: "answer a question: --set domain=example.org (repeatable)"},
					&cli.BoolFlag{Name: "no-prompt", Usage: "ask nothing: every question not --set takes its default, and [name] is required"},
					&cli.BoolFlag{Name: "force", Usage: "overwrite files the stack's directory has already"},
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
						Usage:     "route /apis/<api>/* to an upstream: dev.yaml's example API on the host by default",
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
				Name:  "example",
				Usage: "run the example API on the host, with its rendered settings",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "dir", Value: "examples/compute", Usage: "the example, in the stack"},
					&cli.StringFlag{Name: "lib-src", Usage: "a lib checkout to run it against, instead of the version its go.mod pins"},
				},
				Action: withStack(func(ctx context.Context, cmd *cli.Command, s *dev.Stack) error {
					return s.Example(ctx, cmd.String("dir"), cmd.String("lib-src"))
				}),
			},
			{
				Name:  "check",
				Usage: "lint the stack's script, vet and build the example",
				Flags: []cli.Flag{&cli.StringFlag{Name: "dir", Value: "examples/compute", Usage: "the example, in the stack"}},
				Action: withStack(func(ctx context.Context, cmd *cli.Command, s *dev.Stack) error {
					return s.Check(ctx, cmd.String("dir"))
				}),
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
	vars := map[string]any{}
	for _, kv := range cmd.StringSlice("set") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return fmt.Errorf("dev init: --set %q: want key=value", kv)
		}
		vars[k] = v
	}
	dir, err := dev.Init(dev.InitOptions{
		Template: cmd.String("template"),
		Name:     cmd.Args().First(),
		Dir:      cmd.String("dir"),
		Vars:     vars,
		NoPrompt: cmd.Bool("no-prompt"),
		Force:    cmd.Bool("force"),
	}, os.Stdout)
	if err != nil {
		return err
	}
	// a new stack: nothing to overwrite yet, so dev up has nothing to ask
	s, err := dev.OpenStack(dev.StackOptions{Config: filepath.Join(dir, dev.ConfigName)})
	if err != nil {
		return err
	}
	if _, err := s.Render(dev.RenderOptions{Force: true}); err != nil {
		return err
	}
	out := ui.Stdout()
	_, _ = fmt.Fprintf(out, "\n  %s is %s\n\n", ui.Name(dir), ui.OK("ready:"))
	for _, c := range []string{"cd " + dir, "infrapi-cli dev doctor", "infrapi-cli dev up", "infrapi-cli dev contexts"} {
		_, _ = fmt.Fprintf(out, "    %s\n", ui.Cmd(c))
	}
	_, _ = fmt.Fprintln(out)
	return nil
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
	_, _ = fmt.Fprintf(out, "  %s %s, then create an API: %s\n", ui.Bold("next:"), ui.Cmd("infrapi-cli auth whoami"),
		ui.URL("https://infrapi.github.io/products/dev/docs/create-an-api/"))
	return nil
}

func runDevAPI(add bool) func(context.Context, *cli.Command, *dev.Stack) error {
	return func(ctx context.Context, cmd *cli.Command, s *dev.Stack) error {
		c := s.Config
		api, upstream := c.FullAPI(c.Example.API), fmt.Sprintf("%s:%d", s.HostAlias, c.Example.Port)
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
