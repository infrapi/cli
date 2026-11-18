---
title: Getting started
description: Step by step, from nothing to an API project and its local InfraPI - install infrapi-cli, create the project, start its dev stack, log in, and make a first call.
weight: 15
---

# Getting started

From nothing to an API project and the local InfraPI it runs against, in
eight steps. At the end, you have a Go project, `compute`, and Kayra, its
reconciler, etcd and APISIX run in containers on your machine, you logged
in as their administrator.

1. [Install infrapi-cli](#1-install-infrapi-cli)
2. [Check the prerequisites](#2-check-the-prerequisites)
3. [Create the project](#3-create-the-project)
4. [Create its dev stack](#4-create-its-dev-stack)
5. [Start it](#5-start-it)
6. [Log in](#6-log-in)
7. [Make a first call](#7-make-a-first-call)
8. [Next](#8-next)

## 1. Install infrapi-cli

`infrapi-cli` is InfraPI's command line. It creates the project and the
stack, runs the stack, and is the client you call Kayra with: nothing else
to install to drive it, no `make`.

```bash
go install github.com/infrapi/cli/cmd/infrapi-cli@latest
infrapi-cli --version
```

Or take a release archive: see
[installing infrapi-cli](https://infrapi.github.io/cli/getting-started/#1-install).

## 2. Check the prerequisites

| What | Why | Check with |
|---|---|---|
| Go 1.27.3+ | builds and runs your API | `go version` |
| podman 4.7+ **or** docker, with compose | runs the stack's containers | `podman compose version`, or `docker compose version` |
| `make` | your project's targets: `make run`, `make test`... | `make --version` |
| `curl` | only for the `curl` examples of these docs | `curl --version` |

podman is used when it is installed, docker otherwise. To use docker
although podman is installed, add `--engine docker`, or set `ENGINE=docker`
(step 5). With podman, `podman compose` needs a compose provider:
`podman-compose`, or `docker-compose`.

Once the stack exists, `infrapi-cli dev doctor` checks all of this for you.

## 3. Create the project

```bash
infrapi-cli app init compute
```

It asks a few questions. Enter keeps the default:

| Question | Default | What it sets |
|---|---|---|
| Go module | `example.com/compute` | `go.mod`'s module, `github.com/acme/compute` say |
| API group | `compute.infrapi.io` | the API's group: its paths are `/apis/compute.infrapi.io/v1/...`, the name Kayra knows it by |
| CI | GitHub Actions | `.github/workflows/ci.yml`, `.gitlab-ci.yml`, or none: lint, test, build, and the release on a tag |
| A release builds | everything | archives, deb and rpm packages, a container image |
| Image | from the module, `ghcr.io/acme/compute` | where the release pushes the image |
| GOPRIVATE | the module's host | only for a module on a host the Go proxy can't read, `gitlab.example.com/*` |

`--no-prompt` takes every default, and each question has its flag
(`--module`, `--group`, `--ci`, `--package`, `--image`, `--goprivate`), for
a script say. It creates `./compute`, then runs `go mod tidy`:

```text
compute/
  api/compute-v1.yaml        the contract: what the API serves, checked on every request
  api/zz_generated.go        generated from it: its schemas as Go types
  cmd/compute/main.go        the server: infrapi/lib, Kayra's authentication and authorization
  internal/config/           the settings: infrapi/lib's INFRAPI_APP_*, and COMPUTE_*
  internal/compute/          the implementation: Service, a file per resource
  dev.yaml                   the dev stack's settings
  Makefile                   generate, fmt, lint, test, build, run, snapshot
  .goreleaser.yaml, Dockerfile, packaging/   the release
  .github/workflows/ci.yml   the CI, infrapi/ci's
```

The project is yours: `app init` writes it once, and nothing rewrites it
but the generated `zz_generated.go` files. Its contract has no resource yet:
[Develop an API](develop-an-api.md) adds one.

## 4. Create its dev stack

```bash
cd compute
infrapi-cli dev init
```

`dev init` reads `dev.yaml`, written by `app init`, and renders what the
stack runs into `.infrapi-dev/`, which git ignores:

```text
.infrapi-dev/
  compose.yaml       the containers: etcd, kayra, reconciler, apisix, gateway-init, httpbin
  kayra.yaml         Kayra's configuration
  apisix/, routes/   the gateway's configuration and routes
  keys/              the keys Kayra signs tokens with
  app.env            your API's settings: make run reads them
```

`dev.yaml` is the only file you edit. It holds Kayra's domain
(`infrapi.io`), platform (`sandbox`) and region (`eu-west-1`), the ports,
the users of the stack (`admin`, `jdoe`, `nobody`) and your API's port, `app`.
[The dev stack](dev-stack.md#change-the-stack-devyaml) has every setting.
Edit it before `dev init`, or after, then `infrapi-cli dev up --force`
applies the change.

> [!TIP]
> **A local Kayra, without an API project:** `infrapi-cli dev init` in an
> empty directory asks for the domain, platform, region and Kayra image,
> writes `dev.yaml`, then renders the stack.

## 5. Start it

```bash
infrapi-cli dev doctor
infrapi-cli dev up
infrapi-cli dev health
# {"status":"ok"}  kayra
# {"status":"ok"}  gateway
```

`infrapi-cli dev doctor` prints `ok` or `MISSING` for each prerequisite, and how to
install what is missing. `infrapi-cli dev up` pulls the images and starts the
containers: the first start takes a minute, for the pulls.

| Service | On your machine | |
|---|---|---|
| Kayra | `http://localhost:8081` | authentication, RBAC, the API catalog |
| the gateway | `http://localhost:9080` | APISIX, in front of Kayra and your APIs: what you call |
| APISIX's Admin API | `http://localhost:9180` | its routes, and a UI at `/ui/` |

With docker while podman is installed: `infrapi-cli dev up --engine docker`,
or `export ENGINE=docker` once. `infrapi-cli dev --help` lists every command,
and each prints the commands it runs, `+ podman compose ... up -d` say.
They work from any directory of the project.

## 6. Log in

On a real Kayra you log in with `infrapi-cli auth login --server <url>`: your
browser goes through the server's identity provider. The dev stack has no
identity provider, so Kayra signs the tokens itself. `infrapi-cli dev contexts` asks it
for three, and saves each as an `infrapi-cli` context:

```bash
infrapi-cli dev contexts
infrapi-cli auth context list
# CURRENT  NAME        PROVIDER  SERVER                 EXPIRY
# *        dev-admin             http://localhost:9080  -
#          dev-jdoe              http://localhost:9080  -
#          dev-nobody            http://localhost:9080  -
```

| Context | Who | Can |
|---|---|---|
| `dev-admin` (current) | a sysadmin, group `sandbox-core-sysadmin` | everything: namespaces, API registrations, RBAC |
| `dev-jdoe` | the user `jdoe`, group `sandbox-thetenant-admins` | what the RBAC you create grants that group |
| `dev-nobody` | the user `nobody`, no group | nothing: authenticated, authorized for nothing |

**infrapi-cli**

```bash
infrapi-cli auth whoami
infrapi-cli auth whoami --context dev-jdoe
infrapi-cli auth context use dev-jdoe     # make it the current one; auth context use dev-admin to go back
```

**curl**

```bash
ADMIN=$(infrapi-cli auth context token dev-admin)
curl -s -X POST http://localhost:9080/oauth2/userinfo -H "Authorization: Bearer $ADMIN"
```

The tokens last 8 hours: run `infrapi-cli dev contexts` again when they expire.

## 7. Make a first call

The stack is empty: Kayra serves its own API groups, and nothing else is
registered yet. Ask it what it serves, as `dev-admin`:

**infrapi-cli**

```bash
infrapi-cli apis                              # every API group and its resources: Kayra's own, for now
infrapi-cli get system namespaces             # the tenants: none yet
infrapi-cli get catalog apiregistrations      # the registered APIs: none yet
```

**curl**

```bash
GW=http://localhost:9080
curl -s $GW/apis -H "Authorization: Bearer $ADMIN"
curl -s $GW/apis/system.infrapi.io/v1/namespaces -H "Authorization: Bearer $ADMIN"
curl -s $GW/apis/catalog.infrapi.io/v1/apiregistrations -H "Authorization: Bearer $ADMIN"
```

The same calls as `dev-nobody` are refused: try
`infrapi-cli get system namespaces --context dev-nobody`.

## 8. Next

Read the docs in this order:

1. **[Develop an API](develop-an-api.md)**: add a resource to the
   contract, write it, run it behind the gateway, register it in Kayra,
   onboard a tenant, and call it as one of its users.
2. **[The dev stack](dev-stack.md)**: how the stack works, the everyday
   commands, `dev.yaml`, users and tokens, the gateway, and how to start
   over.

`infrapi-cli dev down` stops the stack and keeps its data;
`infrapi-cli dev up` starts it again.
