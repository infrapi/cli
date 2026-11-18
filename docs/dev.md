---
title: dev
description: infrapi-cli dev - create a local InfraPI dev stack from dev.yaml, then run it - up, down, contexts, routes, logs - on podman or docker.
weight: 40
---

# dev

`infrapi-cli dev` is the local InfraPI to develop an API against: Kayra,
its reconciler, etcd and APISIX on podman or docker, configured by one
file, `dev.yaml`, an API project's (`infrapi-cli app init`, see [app](app.md)).
`dev init` creates a stack; every other command runs it. See
[Getting started](develop/getting-started.md) for a walkthrough, and
[the dev stack](develop/dev-stack.md) for what runs and how.

> [!TIP]
> The commands below use `infrapi-cli`: [install it](getting-started.md#1-install).

| Command | What it does | How |
|---|---|---|
| `dev init` | renders the stack beside `dev.yaml`, into `.infrapi-dev/`; writes `dev.yaml` first when there is none | Go |
| `dev doctor` | checks the prerequisites: podman or docker, its compose, go, the proxy | `<engine> --version`, `<engine> compose version`, `go version` |
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
| `dev api add [api [upstream]]` | routes `/apis/<api>/*` through the gateway; `app register` does it for your API | Go, APISIX's Admin API |
| `dev api rm [api]` | removes that route | Go, APISIX's Admin API |
| `dev check` | lints the stack's script | `sh -n` |

Every command but `init` works on the stack whose `dev.yaml` is in the
working directory or a parent, so it runs from anywhere in the project.
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

Compose runs `.infrapi-dev/compose.yaml`, rendered with the rest. With
docker, it also gets `.infrapi-dev/compose.docker.yaml`, which gives the
gateway `host.docker.internal` on Linux. A `compose.override.yaml` beside
`dev.yaml` comes last; its relative paths are from `.infrapi-dev/`,
compose's project directory.

## dev init

```bash
cd compute            # an API project, its dev.yaml written by infrapi-cli app init
infrapi-cli dev init
# dev.yaml: the stack's settings
# compose.yaml: new file
# …
#   …/compute/.infrapi-dev is ready. Edit dev.yaml, then dev up --force applies it.
```

It renders the stack beside `dev.yaml`, into `.infrapi-dev/`, the first
time without asking: nothing is there to overwrite yet. Run again, it is a
[render](#dev-render): it stops on a change without `--force`.

Without `dev.yaml`, in an empty directory say, it writes one first: it asks
for the domain, the platform, the region and Kayra's image, or takes them
from its flags. That is a local Kayra without an API project; `app`'s
settings take their defaults.

| Flag | Default | |
|---|---|---|
| `--domain` | `infrapi.io` | Kayra's API domain, for a new `dev.yaml` |
| `--platform` | `sandbox` | `sandbox`, `preprod` or `prod` |
| `--region` | `eu-west-1` | |
| `--image` | `ghcr.io/infrapi/kayra:latest` | the image Kayra runs |
| `--no-prompt` | off | ask nothing: what no flag gives takes its default; asked nothing either without a terminal |
| `--force` | off | write the render's changes when `.infrapi-dev/` exists |

```bash
infrapi-cli dev init --no-prompt --domain example.org --platform preprod
```

Everything else, the ports, the users, your API's port, is in `dev.yaml`:
edit it, then `infrapi-cli dev up --force`. `dev.yaml` is never written
again: it is yours, committed with the project; `.infrapi-dev/` is
ignored by git.

## dev up

```bash
infrapi-cli dev up [--new-keys] [--force]
# + podman compose --env-file .infrapi-dev/env -f .infrapi-dev/compose.yaml ps -q
# + podman compose --env-file .infrapi-dev/env -f .infrapi-dev/compose.yaml up -d
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
on the files (`ps`, `logs`, `down`, `contexts`...) take them as
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
`gateway.routes`, `gateway.config`, `app.env`, `compose.env`), see
[Override anything](develop/dev-stack.md#override-anything).

A new file is only named (`kayra.yaml: new file`). The diff never prints a signing key: a key shows as its fingerprint,
`-----PRIVATE KEY sha256:…-----`, and a new one as
`keys/self.pem: new signing key sha256:…`.

| File | |
|---|---|
| `env` | the domain, image and ports, for `compose --env-file` |
| `kayra.yaml`, `users.yaml` | Kayra's configuration and users, mounted into both Kayra services |
| `keys/*.pem` | the signing keys, generated once and kept; `--new-keys` generates new ones, refusing every token minted before |
| `routes/*.json`, `routes/removed` | the gateway's own routes, and the ids `dev.yaml` removes |
| `app.env` | your API's settings, `dev.yaml`'s `app`: `make run` reads them |
| `.gitignore` | `*`: git ignores the directory |
| `compose.yaml`, `compose.docker.yaml` | the containers, compose's files |
| `apisix/config.yaml`, `apisix/routes.sh` | APISIX's configuration (`gateway.config` merged in), and the script loading the routes |
| `kayra/Dockerfile` | Kayra built from a checkout, for `kayra.src` |

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
Kayra reads included. `api` defaults to `dev.yaml`'s `app.api` (a name
without a dot is `<name>.<domain>`), `upstream` to
`<host-alias>:<app.port>`, the host as the containers see it.
`infrapi-cli app register` routes your API the same way, then registers it
in Kayra.

