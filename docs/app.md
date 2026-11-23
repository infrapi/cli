---
title: app
description: infrapi-cli app - create an API project on infrapi/lib, add resources to its contract, generate its Go code, run it, and register it in Kayra.
weight: 35
---

# app

`infrapi-cli app` writes and grows an API project: a Go module on
[infrapi/lib](https://github.com/infrapi/lib), its OpenAPI contract, its CI
and its release. What it writes is the project's from then on: only the
`zz_generated.go` files are written again, from the contract. See
[Develop an API](develop/develop-an-api.md) for a walkthrough.

> [!TIP]
> The commands below use `infrapi-cli`: [install it](getting-started.md#1-install).

| Command | What it does | How |
|---|---|---|
| `app init [name]` | creates the project in `./<name>`, its `dev.yaml` and its generated code | Go, then `go mod tidy` |
| `app add <resource>` | adds a resource to the contract, generates, and writes its operations, 501 each | Go |
| `app generate` | writes the Go code from the contracts: `api/zz_generated.go`, `internal/<name>/zz_generated.go` | Go |
| `app run` | runs the API against the dev stack, with `.infrapi-dev/app.env` | `go run ./cmd/<name>` (`os/exec`) |
| `app register` | routes the dev stack's gateway to the API, then registers it in Kayra | Go, APISIX's Admin API and Kayra's catalog |
| `app keygen <file>` | writes an identity key for the API, and prints its public half | Go |

Every command but `init` works on the project the working directory is
in: the first directory up with a `go.mod` and an `api/`.

## app init

```bash
infrapi-cli app init compute
```

It asks for what no flag gives, then writes `./compute`:

| Flag | Default | |
|---|---|---|
| `--module` | `example.com/<name>` | the Go module |
| `--group` | `<name>.infrapi.io` | the API group: its paths are `/apis/<group>/v1/...` |
| `--ci` | `github` | `github` (`.github/workflows/ci.yml`), `gitlab` (`.gitlab-ci.yml`) or `none`: both import [infrapi/ci](https://github.com/infrapi/ci) |
| `--package` | every one | what a release builds: `archive`, `deb`, `rpm`, `image`, or `none`; repeatable |
| `--image` | from the module | the image, without a tag: `ghcr.io/<owner>/<name>` for `github.com`, the project's registry with `--ci gitlab` |
| `--goprivate` | asked for a module host the Go proxy can't read | `GOPRIVATE` in the Makefile and the CI: `gitlab.example.com/*` |
| `--dir` | `.` | where `./<name>` is created |
| `--no-prompt` | off | ask nothing: what no flag gives takes its default; `[name]` is required. Asked nothing either without a terminal |

```text
compute/
  api/api.go                 package api: //go:generate infrapi-cli app generate
  api/compute-v1.yaml        the contract, OpenAPI 3.0, no path yet
  api/zz_generated.go        generated: the schemas as types, Group, Roots, Contracts
  cmd/compute/main.go        the server: config, logger, Kayra's auth, the contracts checked, Register
  internal/config/config.go  the settings: infrapi/lib's INFRAPI_APP_*, COMPUTE_AUTH_URL, how to add yours
  internal/compute/compute.go       Service, the implementation, and New
  internal/compute/zz_generated.go  generated: Handlers, Register, notImplemented
  dev.yaml                   the dev stack's settings, app.api the group: infrapi-cli dev init reads it
  Makefile                   generate, fmt, lint, test, build, run, snapshot
  .golangci.yml, .gitignore
  .goreleaser.yaml           archives, deb and rpm (packaging/), the image (Dockerfile)
  .github/workflows/ci.yml   or .gitlab-ci.yml
```

Then `go mod tidy`, when Go is installed, with the project's `GOPRIVATE`.
The project starts on lib `v0.19.0` and Go 1.27.3.

## app add

```bash
infrapi-cli app add vm [--singular vm] [--plural vms] [--kind VM] [--cluster] [--version v1]
```

The resource is named singular or plural, `vm` or `vms`: the other form is
inferred, by an English rule of thumb (`policy`, `policies`;
`environments`, `environment`), and printed first. When it gets one wrong,
set it: `infrapi-cli app add cache --plural caches`, `infrapi-cli app add
--singular person --plural people`. `--kind` is the schema's, the Go
type's name, from the singular.

It adds to the contract the resource's paths, `.../namespaces/{namespace}/vms`
and `.../vms/{name}` (`.../vms` with `--cluster`), with five operations,
`listVMs`, `createVM`, `getVM`, `replaceVM` and `deleteVM`, and its schema,
`VM`, a `name` and a `created_at`. The contract's other lines, comments
included, are kept as they are. Then it generates, and writes
`internal/<name>/vms.go`, the operations answering `501 Not Implemented`,
and `vms_test.go`, a test of each.

It refuses a resource whose paths or schema the contract has, and never
writes over `vms.go` or `vms_test.go`. With more than one contract,
`--version` names the one.

## app generate

```bash
infrapi-cli app generate     # or go generate ./...
```

It reads the contracts, `api/<name>-<version>.yaml`, and writes:

- `api/zz_generated.go`: `Group`, `Roots`, the contracts embedded and
  `Contracts()`, as `server.WithOpenAPI` takes them, and a Go type per schema;
- `internal/<name>/zz_generated.go`: `Handlers`, a method per operation,
  `Register(r gin.IRouter, h Handlers)`, their routes, and `notImplemented`.

What it requires of a contract: OpenAPI 3.0; `info.title`, the API's name
and the file's (`compute` for `compute-v1.yaml`); `info.x-infrapi-group`,
its group, every path under `/apis/<group>/<version>`; an `operationId` on
every operation; JSON bodies; a `$ref` to `#/components/schemas/<Name>`, or
a plain type, for a body or a response. The types are in
[Develop an API](develop/develop-an-api.md#3-describe-it-in-the-contract).

A response that is not JSON, a download, is a `type: string, format:
binary` schema under its media type (`application/gzip`, say): its handler
returns an `io.ReadCloser`, streamed with that media type.

It refuses two path parameters of different names at the same place
(`.../environments/{name}` beside `.../environments/{environment}/modules`):
gin routes a place under one name, and would panic at start.

## app run

```bash
infrapi-cli app run
# + go run ./cmd/compute
```

Runs the API against the dev stack, `go run ./cmd/<name>`, with
`.infrapi-dev/app.env` as `INFRAPI_CONFIG_DOTENV_FILE` and in its
environment. It refuses a `.infrapi-dev/` that is not `dev.yaml`'s
(`infrapi-cli dev up --force` applies it), and stops with Ctrl-C.

## app register

```bash
infrapi-cli app register [--owner dev] [--upstream host:port] [--no-route] [--jwks <file> | --no-identity]
```

With the project's `dev.yaml`, it routes the dev stack's gateway to the API
first, `/apis/<group>/*` to `<host-alias>:<app.port>` (as `dev api add`),
or to `--upstream`; `--no-route` leaves the routes alone. Then it registers
the API in Kayra, as the current context (`--context` for another):

```json
{"name": "compute.infrapi.io", "short_name": "compute", "version": "v1", "active": true, "owner": "dev", "description": "The compute API.",
 "identity": {"jwks_from": "spec"}}
```

Registered again, the API gets a new `sync_id`: Kayra reads its spec again,
and resyncs the namespaces subscribed to it.

`identity` is the API's [own identity](https://infrapi.github.io/kayra/architecture/api-identity/),
`system:api:<group>`:

- **By default, `jwks_from: spec`:** Kayra reads the keys the API serves at
  `-/jwks.json`, with its spec. An API that serves none gets no token.
- **`--jwks <file>`:** pins the keys instead, from a JWK set file (what
  `app keygen` prints) or a PEM key, whose public half only is sent.
- **`--no-identity`:** registers the API without one.

## app keygen

```bash
infrapi-cli app keygen /etc/compute/identity.pem > compute-jwks.json
# /etc/compute/identity.pem written, kid hSO0YDfX…: INFRAPI_IDENTITY_KEY_PATH=/etc/compute/identity.pem
```

Writes a new EC P-256 key, PKCS#8 PEM, mode 0600: the API's identity key,
which its `internal/config` reads from `INFRAPI_IDENTITY_KEY_PATH`. It
prints the public half on stdout, a JWK set whose `kid` is the key's RFC 7638
thumbprint. It refuses to replace a file unless `--force`. The dev stack
makes one of its own, `.infrapi-dev/keys/app-identity.pem`.
