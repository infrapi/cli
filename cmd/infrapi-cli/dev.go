package main

import (
	"context"
	"fmt"
	"os"
	"strings"

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
				Usage:  "render .infrapi-dev/ from dev.yaml: Kayra's configuration and keys, the gateway's routes, the env compose reads",
				Flags:  []cli.Flag{newKeysFlag()},
				Action: withStack(runDevRender),
			},
			{
				Name:   "build",
				Usage:  "build kayra-dev:local from dev.yaml's kayra.src",
				Action: withStack(func(ctx context.Context, cmd *cli.Command, s *dev.Stack) error { return s.Build(ctx) }),
			},
			{
				Name:  "up",
				Usage: "render, build if kayra.src is set, start the stack; recreate Kayra when its configuration changed",
				Flags: []cli.Flag{newKeysFlag()},
				Action: withStack(func(ctx context.Context, cmd *cli.Command, s *dev.Stack) error {
					return s.Up(ctx, cmd.Bool("new-keys"))
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
	fmt.Printf("\n  %s is ready:\n\n    cd %s\n    infrapi-cli dev doctor\n    infrapi-cli dev up\n    infrapi-cli dev contexts\n\n", dir, dir)
	return nil
}

func runDevDoctor(ctx context.Context, _ *cli.Command, s *dev.Stack) error {
	missing := 0
	for _, c := range s.Doctor(ctx) {
		state := "ok"
		switch {
		case !c.OK && c.Optional:
			state = "optional"
		case !c.OK:
			state = "MISSING"
			missing++
		}
		fmt.Printf("  %-8s %-8s %s\n", state, c.Name, c.Detail)
	}
	fmt.Printf("\n  engine %s, the host seen from the containers as %s\n", s.Engine, s.HostAlias)
	if missing > 0 {
		return fmt.Errorf("doctor: %d prerequisite(s) missing", missing)
	}
	return nil
}

func runDevRender(_ context.Context, cmd *cli.Command, s *dev.Stack) error {
	if _, err := s.Render(cmd.Bool("new-keys")); err != nil {
		return err
	}
	image := s.Config.Kayra.Image
	if s.Config.Kayra.Src != "" {
		image = "built from " + s.Config.Kayra.Src
	}
	fmt.Printf("render: %s, domain %s, kayra %s\n", s.Out, s.Config.Domain, image)
	return nil
}

func runDevContexts(ctx context.Context, _ *cli.Command, s *dev.Stack) error {
	names, err := s.Contexts(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("contexts: %s saved, on http://localhost:%d; dev-admin is the current one\n", strings.Join(names, ", "), s.Config.Ports.Gateway)
	fmt.Println("  next:   infrapi-cli auth whoami, then create an API: https://infrapi.github.io/products/dev/docs/create-an-api/")
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
			fmt.Printf("api: %s removed\n", api)
			return nil
		}
		if err := g.AddAPI(ctx, api, upstream); err != nil {
			return err
		}
		fmt.Printf("api: /apis/%s/* -> %s\n", api, upstream)
		return nil
	}
}
