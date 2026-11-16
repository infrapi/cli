---
title: dev
description: infrapi-cli dev - scaffold a local InfraPI dev stack from the infrapi/dev template, render it from dev.yaml, kickstart Kayra, route an API.
weight: 40
---

# dev

`infrapi-cli dev` is the local InfraPI to develop an API against: Kayra,
its reconciler, etcd and APISIX on podman, configured by one file,
`dev.yaml`. `dev init` creates the stack; its Makefile runs the other
commands. See [the dev stack](https://infrapi.github.io/dev/dev-stack/) for
what runs and how.

| Command | What it does | Run by |
|---|---|---|
| `dev init [name]` | scaffold a new stack in `./<name>` | you |
| `dev render` | render `.infrapi-dev/` from `dev.yaml` | `make render`, `make up` |
| `dev apply` | create `dev.yaml`'s APIs and tenants in Kayra, sync every API | `make kickstart` |
| `dev api add [api [upstream]]` | route `/apis/<api>/*` through the gateway | `make api-add` |
| `dev api rm [api]` | remove that route | `make api-rm` |

## dev init

```bash
infrapi-cli dev init mystack
# Project Name  mystack
# Domain        infrapi.io
# Platform      sandbox
# Region        eu-west-1
# Kayra image   ghcr.io/infrapi/kayra:latest
#
#   mystack is ready:
#
#     cd mystack && make up && make contexts
```

It renders [infrapi/dev](https://github.com/infrapi/dev), a
[scaffold](https://github.com/hay-kot/scaffold), into `./<name>`. It asks
for the domain, the platform, the region and Kayra's image, and writes the
answers into the stack's `dev.yaml`. The rest of the stack is copied as
it is. The template is cloned once into `~/.cache/infrapi/templates`
(the OS's user cache directory), and reused after.

| Flag | Default | |
|---|---|---|
| `--template` | `https://github.com/infrapi/dev` | the scaffold: a git URL, `…/dev@v1.2.0` for a tag, `…/dev#subdir` for a directory in it, or a local path |
| `--dir` | `.` | where the stack's directory is created |
| `--set key=value` | | answer a question (`domain`, `platform`, `region`, `image`); repeatable |
| `--no-prompt` | off | ask nothing: every question not `--set` takes its default, and `[name]` is required |
| `--force` | off | overwrite the files the stack's directory has already; without it, they are left as they are |

Without a terminal to prompt in, a CI job say:

```bash
infrapi-cli dev init --no-prompt --set domain=example.org --set platform=preprod mystack
```

Everything else, the ports, the users, the APIs and tenants to start
from, is in `dev.yaml`: edit it, then `make up`.

## dev render

```bash
infrapi-cli dev render [--config dev.yaml] [--out .infrapi-dev] [--new-keys]
```

It writes what the stack runs with into `.infrapi-dev/`:

| File | |
|---|---|
| `env` | the domain, image and ports, for `podman compose --env-file` and the Makefile |
| `kayra.yaml`, `users.yaml` | Kayra's configuration and users, mounted into both Kayra services |
| `keys/*.pem` | the signing keys, generated once and kept; `--new-keys` generates new ones, refusing every token minted before |
| `routes/*.json` | the gateway's own routes |
| `example.env` | the example API's settings |

A file is written only when its content changes, so `make up` recreates
Kayra only when its configuration did. `dev.yaml` is checked first: an
unknown key, a malformed name, a platform infrapi/lib does not take, or a
port used twice is refused. `--config` defaults to `$DEV_CONFIG`, then
`dev.yaml`.

## dev apply

```bash
infrapi-cli dev apply [--config dev.yaml] [--server URL] [--token TOKEN]
```

It creates `dev.yaml`'s `apis` (`<name>.<domain>`, their owner, version
and short name) and `tenants` (the namespace, its admins' cluster RBAC on
`catalog.<domain>` and `authorization.<domain>`, a read-write role in the
namespace, the subscriptions), then syncs every API. It talks to the
gateway of `dev.yaml` unless `--server` says otherwise, with `--token`,
`$INFRAPI_TOKEN`, or the `dev-admin` context's token (`make contexts`).

It reads Kayra's groups from its `/.well-known/kayra.json`, and stops if
Kayra's domain is not `dev.yaml`'s. You can run it again: Kayra's creates
are upserts, and a subscription that exists already is left as it is,
not reset to `init`.

## dev api

```bash
infrapi-cli dev api add [--config dev.yaml] [api [upstream]]
infrapi-cli dev api rm  [--config dev.yaml] [api]
```

`add` routes `/apis/<api>/*` to `upstream` through APISIX's Admin API:
the API, and its own endpoints under `/apis/<api>/<version>/-/`, the spec
Kayra reads included. `api` defaults to `dev.yaml`'s example API (a name
without a dot is `<name>.<domain>`), `upstream` to
`host.containers.internal:<example.port>`, the host as podman's containers
see it. `--admin` is the Admin API, `dev.yaml`'s by default.
