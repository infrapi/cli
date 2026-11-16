package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/infrapi/cli/internal/clicontext"
	"github.com/infrapi/cli/internal/dev"
)

// devCommand is the local dev stack: scaffold one (init), and what its
// Makefile runs (render, apply, api).
func devCommand() *cli.Command {
	configFlag := &cli.StringFlag{
		Name: "config", Value: "dev.yaml", Sources: cli.EnvVars("DEV_CONFIG"),
		Usage: "the stack's settings",
	}
	return &cli.Command{
		Name:  "dev",
		Usage: "a local InfraPI to develop an API against: Kayra, etcd and APISIX on podman",
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
				Name:   "render",
				Usage:  "render .infrapi-dev/ from dev.yaml: Kayra's configuration and keys, the gateway's routes, the env compose and make read",
				Flags:  []cli.Flag{configFlag, &cli.StringFlag{Name: "out", Value: ".infrapi-dev", Usage: "where the files go"}, &cli.BoolFlag{Name: "new-keys", Usage: "generate new signing keys: every token minted before is refused"}},
				Action: runDevRender,
			},
			{
				Name:   "apply",
				Usage:  "create dev.yaml's APIs and tenants in Kayra, then sync every API (make kickstart)",
				Flags:  []cli.Flag{configFlag, &cli.StringFlag{Name: "server", Usage: "Kayra, or the gateway in front of it (default: dev.yaml's gateway)"}, &cli.StringFlag{Name: "token", Sources: cli.EnvVars("INFRAPI_TOKEN"), Usage: "a sysadmin's bearer token (default: the dev-admin context's)"}},
				Action: runDevApply,
			},
			{
				Name:  "api",
				Usage: "route an API through the stack's gateway",
				Commands: []*cli.Command{
					{
						Name:      "add",
						Usage:     "route /apis/<api>/* to an upstream: dev.yaml's example API on the host by default",
						ArgsUsage: "[api [upstream]]",
						Flags:     []cli.Flag{configFlag, &cli.StringFlag{Name: "admin", Usage: "APISIX's Admin API (default: dev.yaml's)"}},
						Action:    runDevAPI(true),
					},
					{
						Name:      "rm",
						Usage:     "remove an API's route",
						ArgsUsage: "[api]",
						Flags:     []cli.Flag{configFlag, &cli.StringFlag{Name: "admin", Usage: "APISIX's Admin API (default: dev.yaml's)"}},
						Action:    runDevAPI(false),
					},
				},
			},
		},
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
	fmt.Printf("\n  %s is ready:\n\n    cd %s && make up && make contexts\n\n", dir, dir)
	return nil
}

func runDevRender(_ context.Context, cmd *cli.Command) error {
	c, err := dev.LoadConfig(cmd.String("config"))
	if err != nil {
		return err
	}
	out := cmd.String("out")
	if err := dev.Render(c, out, cmd.Bool("new-keys")); err != nil {
		return err
	}
	image := c.Kayra.Image
	if c.Kayra.Src != "" {
		image = "built from " + c.Kayra.Src
	}
	fmt.Printf("render: %s, domain %s, kayra %s\n", out, c.Domain, image)
	return nil
}

func runDevApply(ctx context.Context, cmd *cli.Command) error {
	c, err := dev.LoadConfig(cmd.String("config"))
	if err != nil {
		return err
	}
	token := cmd.String("token")
	if token == "" {
		saved, err := resolveContextNamed("dev-admin")
		if err != nil {
			return errors.New("dev apply: --token, or a dev-admin context (make contexts): a sysadmin's token")
		}
		token = saved.Token
	}
	server := cmd.String("server")
	if server == "" {
		server = fmt.Sprintf("http://localhost:%d", c.Ports.Gateway)
	}
	return dev.Apply(ctx, c, &dev.Kayra{Server: server, Token: token}, os.Stdout)
}

func runDevAPI(add bool) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		c, err := dev.LoadConfig(cmd.String("config"))
		if err != nil {
			return err
		}
		admin := cmd.String("admin")
		if admin == "" {
			admin = fmt.Sprintf("http://localhost:%d", c.Ports.Admin)
		}
		api, upstream := c.FullAPI(c.Example.API), fmt.Sprintf("host.containers.internal:%d", c.Example.Port)
		if cmd.Args().Len() > 0 {
			api = c.FullAPI(cmd.Args().Get(0))
		}
		if cmd.Args().Len() > 1 {
			upstream = cmd.Args().Get(1)
		}
		g := &dev.Gateway{URL: admin, Key: dev.AdminKey}
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

// resolveContextNamed is the saved context name, as --context name would
// pick it.
func resolveContextNamed(name string) (clicontext.Context, error) {
	f, err := clicontext.Load()
	if err != nil {
		return clicontext.Context{}, err
	}
	c, ok := f.Contexts[name]
	if !ok {
		return clicontext.Context{}, fmt.Errorf("no such context %q", name)
	}
	return c, nil
}
