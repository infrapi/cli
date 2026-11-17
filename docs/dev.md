---
title: dev
description: infrapi-cli dev - scaffold a local InfraPI dev stack from the infrapi/dev template, then run it - up, down, contexts, routes, logs - on podman or docker.
weight: 40
---

# dev

`infrapi-cli dev` is the local InfraPI to develop an API against: Kayra,
its reconciler, etcd and APISIX on podman or docker, configured by one
file, `dev.yaml`. `dev init` creates a stack; every other command runs it.
See [Getting started](https://infrapi.github.io/dev/getting-started/) for a
walkthrough, and [the dev stack](https://infrapi.github.io/dev/dev-stack/)
for what runs and how.

> [!TIP]
> The commands below use `infrapi-cli`: [install it](getting-started.md#1-install).

| Command | What it does | How |
|---|---|---|
| `dev init [name]` | scaffolds a new stack in `./<name>` | Go, [scaffold](https://github.com/hay-kot/scaffold) |
| `dev doctor` | checks the prerequisites: podman or docker, its compose, go | `<engine> --version`, `<engine> compose version`, `go version` |
| `dev render [--new-keys] [--force]` | shows what `dev.yaml` changes in `.infrapi-dev/`, as a diff; `--force` writes it | Go |
| `dev build` | builds `kayra-dev:local` from `kayra.src` | `podman build --format docker`, or `docker build` |
| `dev up [--new-keys] [--force]` | renders, builds if `kayra.src` is set, starts the stack; stops on a change to `.infrapi-dev/` without `--force` | `<engine> compose up -d` |
| `dev down` | stops the stack, keeps etcd's data | `compose down` |
| `dev clean` | stops it and wipes etcd | `compose down -v` |
| `dev reset-kayra` | wipes Kayra's objects only, keeps the routes and keys | `compose exec etcd etcdctl del --prefix /kayra`, `compose restart kayra reconciler` |
| `dev restart [service...]` | restarts services, or every one | `compose restart` |
| `dev ps` | lists the containers | `compose ps` |
| `dev logs [service...]` | follows the logs, of every service or those named | `compose logs -f` |
| `dev health` | checks Kayra and the gateway answer | Go, two GETs |
| `dev routes` | puts the stack's own gateway routes back | Go, APISIX's Admin API |
| `dev token [--subject jdoe]` | prints a token for a user of `dev.yaml` | `compose exec kayra kayra token mint` |
| `dev contexts` | saves `dev-admin` and `dev-<user>` contexts on the gateway | `kayra token mint` as above, then Go |
| `dev api add [api [upstream]]` | routes `/apis/<api>/*` through the gateway | Go, APISIX's Admin API |
| `dev api rm [api]` | removes that route | Go, APISIX's Admin API |
| `dev example [--lib-src DIR]` | runs the example API on the host | `go run .` |
| `dev check` | lints the stack's script, vets and builds the example | `sh -n`, `go vet`, `go build` |

Every command but `init` works on the stack whose `dev.yaml` is in the
working directory or a parent, so it runs from anywhere in the stack.
Every external command is printed first, prefixed by `+`.

## Options of every command

`dev` takes them, before or after the command (`infrapi-cli dev up --engine docker`),
or from the environment:

| Flag | Variable | Default | |
|---|---|---|---|
| `--config` | `DEV_CONFIG` | `dev.yaml` here or in a parent | the stack's `dev.yaml` |
| `--engine` | `ENGINE` | podman when installed, docker otherwise | the container engine |
| `--compose` | `COMPOSE` | `<engine> compose` | the compose command; the stack's `--env-file` and `-f` files are added to it |
| `--build-command` | `BUILD` | `podman build --format docker`, or `docker build` | the image build command |
| `--host-alias` | `HOST_ALIAS` | `host.containers.internal`, or `host.docker.internal` | the host's name in the containers, where `dev api add` routes |

With docker, compose also gets the stack's `compose.docker.yaml`, which
gives the gateway `host.docker.internal` on Linux.

## dev init

```bash
infrapi-cli dev init compute
# Project Name  compute
# Domain        infrapi.io
# Platform      sandbox
# Region        eu-west-1
# Kayra image   ghcr.io/infrapi/kayra:latest
#
#   compute is ready:
#
#     cd compute
#     infrapi-cli dev doctor
#     infrapi-cli dev up
#     infrapi-cli dev contexts
```

It renders [infrapi/dev](https://github.com/infrapi/dev), a
[scaffold](https://github.com/hay-kot/scaffold), into `./<name>`. It asks
for the domain, the platform, the region and Kayra's image, and writes the
answers into the stack's `dev.yaml`. The rest of the stack is copied as
it is. The template is cloned into `~/.cache/infrapi/templates`
(the OS's user cache directory), then pulled at every `dev init`, so a
new stack starts from the template's latest commit. A template pinned to
a tag (`…/dev@v1.2.0`) is not pulled. Offline, the pull fails with a
warning and the cached copy is used.

| Flag | Default | |
|---|---|---|
| `--template` | `https://github.com/infrapi/dev` | the scaffold: a git URL, `…/dev@v1.2.0` for a tag, `…/dev#subdir` for a directory in it, or a local path |
| `--dir` | `.` | where the stack's directory is created |
| `--set key=value` | | answer a question (`domain`, `platform`, `region`, `image`); repeatable |
| `--no-prompt` | off | ask nothing: every question not `--set` takes its default, and `[name]` is required |
| `--force` | off | overwrite the files the stack's directory has already; without it, they are left as they are |

Without a terminal to prompt in, a CI job say:

```bash
infrapi-cli dev init --no-prompt --set domain=example.org --set platform=preprod compute
```

Everything else, the ports, the users, the example API, is in
`dev.yaml`: edit it, then `infrapi-cli dev up --force`. `dev init`
renders `.infrapi-dev/` once, so the first `dev up` needs no `--force`.

## dev up

```bash
infrapi-cli dev up [--new-keys] [--force]
# + podman compose --env-file .infrapi-dev/env -f compose.yaml ps -q
# + podman compose --env-file .infrapi-dev/env -f compose.yaml up -d
```

It renders `.infrapi-dev/` (see [dev render](#dev-render)), builds
`kayra-dev:local` when `kayra.src` is set, and starts the stack. When the
stack was running and the render changed what Kayra, its reconciler or
`gateway-init` read (`kayra.yaml`, `users.yaml`, the routes), it recreates
those three: compose alone would not, since only a file they mount changed.

Without `--force`, a render that would change a file in `.infrapi-dev/`,
from `dev.yaml` or from an edit made there by hand, is canceled before
anything starts: see [dev render](#dev-render).

## dev render

```bash
infrapi-cli dev render [--new-keys] [--force]
```

It shows what `dev.yaml` changes in `.infrapi-dev/`, in the stack's
directory, as a diff, and writes nothing. `--force` writes it, and drops
any edit made by hand there.

Every render works that way, `dev up`'s included: a render that would
create, change or remove a file is canceled without `--force`. It prints
the diff, says which setting of `dev.yaml` each changed file comes from,
and exits 1. `dev init` renders the new stack once. The commands that run
on the files (`ps`, `logs`, `down`, `contexts`, `example`...) take them as
they are, with a warning when they differ from `dev.yaml`.

```bash
infrapi-cli dev render
# --- a/kayra.yaml
# +++ b/kayra.yaml
# @@ -66,7 +66,7 @@
#  reconciler:
# -  max_concurrent_reconciles: 50
# +  max_concurrent_reconciles: 5
#
# render canceled: nothing written to …/compute/.infrapi-dev: it differs from what dev.yaml renders, the diff above.
#
#   The files in .infrapi-dev are rendered from dev.yaml: change dev.yaml, not them. Each file, from dev.yaml's:
#     kayra.yaml   domain, platform, region, location, sysadmin_groups, ports.kayra, users; anything else: kayra.config
#
#   To keep what .infrapi-dev has, an edit made by hand say, add this to dev.yaml:
#
#     kayra:
#       config:
#         reconciler:
#           max_concurrent_reconciles: 50
#
#   Then, or to write dev.yaml's change as it is:
#     infrapi-cli dev up --force   (or infrapi-cli dev render --force)
```

When a file differs from the render, an edit made by hand say, the
message also gives what to add to `dev.yaml` to keep it: every rendered
file takes overrides from `dev.yaml` (`kayra.config`, `kayra.keys`,
`gateway.routes`, `example.env`, `compose.env`), see
[Override anything](https://infrapi.github.io/dev/dev-stack/#override-anything).

A new file is only named (`kayra.yaml: new file`). The diff never prints a signing key: a key shows as its fingerprint,
`-----PRIVATE KEY sha256:…-----`, and a new one as
`keys/self.pem: new signing key sha256:…`.

| File | |
|---|---|
| `env` | the domain, image and ports, for `compose --env-file` |
| `kayra.yaml`, `users.yaml` | Kayra's configuration and users, mounted into both Kayra services |
| `keys/*.pem` | the signing keys, generated once and kept; `--new-keys` generates new ones, refusing every token minted before |
| `routes/*.json`, `routes/removed` | the gateway's own routes, and the ids `dev.yaml` removes |
| `example.env` | the example API's settings |

`dev.yaml` is checked
first: an unknown key, a malformed name, a platform infrapi/lib does not
take, or a port used twice is refused.

## dev contexts

```bash
infrapi-cli dev contexts
# contexts: dev-jdoe, dev-nobody, dev-admin saved, on http://localhost:9080; dev-admin is the current one
```

The stack has no identity provider: Kayra signs the tokens itself. It mints
one per user of `dev.yaml` with its `local` provider, the groups coming from
`dev.yaml`, and saves each as `dev-<id>`. It also mints `dev-admin` with its
`self` provider and the first of `sysadmin_groups`, and makes it the current
context. The tokens last 8 hours: run it again when they expire.
`dev token --subject <id>` prints one token instead of saving it.

## dev api

```bash
infrapi-cli dev api add [api [upstream]]
infrapi-cli dev api rm  [api]
```

`add` routes `/apis/<api>/*` to `upstream` through APISIX's Admin API:
the API, and its own endpoints under `/apis/<api>/<version>/-/`, the spec
Kayra reads included. `api` defaults to `dev.yaml`'s example API (a name
without a dot is `<name>.<domain>`), `upstream` to
`<host-alias>:<example.port>`, the host as the containers see it.

## dev example

```bash
infrapi-cli dev example [--dir examples/compute] [--lib-src ../lib]
```

It runs the example API on the host, with `go run .`, its settings rendered
from `dev.yaml` (`.infrapi-dev/example.env`). `--lib-src` runs it against a
lib checkout through a `go.work` in `.infrapi-dev/`, to try a change to lib.
