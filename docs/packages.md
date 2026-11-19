---
title: Packages
description: infrapi-cli's internal packages - clicontext, its saved logins, oauth2login, the login at Kayra's authorization server, app, the API project generator, and dev, the local dev stack.
weight: 50
---

# Packages

| Package | What it is |
|---|---|
| [`internal/clicontext`](#clicontext) | `~/.config/infrapi/config`: the saved logins, kubeconfig-style contexts |
| [`internal/oauth2login`](#oauth2login) | the login at Kayra's authorization server, its tokens renewed and revoked |
| `internal/netproxy` | `https_proxy`, `http_proxy`, `no_proxy` for every call, read on each request: `http.DefaultTransport`, which go-git uses too |
| `internal/ui` | the terminal output: styles (lipgloss), code highlighting (chroma), colors on a terminal only, off with `NO_COLOR` or `--no-color` |
| [`internal/dev`](#dev) | `dev.yaml`, the files rendered from it, the stack's containers, the gateway routes, `dev init` |

## clicontext

```go
import "github.com/infrapi/cli/internal/clicontext"
```

Persists `cmd/infrapi-cli`'s own login state to `~/.config/infrapi/config` — which
server a token belongs to, and the token itself — in the same
current-context/contexts shape `kubectl` keeps in `~/.kube/config`.
[`infrapi-cli auth login`](commands.md#login) writes to it on every successful
login; [`infrapi-cli auth context`](commands.md#context) reads and switches it.

```go
f, err := clicontext.Load()               // reads Path(), or an empty File if it doesn't exist yet

f.SetContext("kayra-internal", clicontext.Context{
	Server:    server,
	Token:     result.AccessToken,
	TokenType: result.TokenType,
	Expiry:    result.Expiry,
})                                         // upserts and switches CurrentContext to it

err = f.Save()                             // writes back to Path(), dir 0700 / file 0600

name, ctx, err := f.Current()              // errors if nothing has ever logged in
err = f.Use("self")                        // switch without touching credentials
err = f.Delete("self")                     // drop a saved context
names := f.Names()                         // sorted, for stable `auth context list` output
```

`Path()` resolves `$INFRAPI_CONTEXT_FILE` first, then
`$XDG_CONFIG_HOME/infrapi/config`, then `~/.config/infrapi/config` — not to
be confused with `$KAYRA_CONFIG_FILE`/`--config`, which names the
**server's** `kayra.yaml`. The file holds bearer tokens, so `Save`
creates its parent directory at `0700` and writes the file itself at
`0600`.

## oauth2login

infrapi-cli's OAuth 2.1 client of Kayra's
[authorization server](https://infrapi.github.io/kayra/architecture/oauth2-server/):
it knows nothing but a server URL, and reads its endpoints from
`/.well-known/oauth-authorization-server`. infrapi-cli is Kayra's built-in
client, `infrapi-cli`: public, first-party (no consent page).

```go
r, err := oauth2login.LoginToServer(ctx, "https://kayra.example.com", os.Stderr)
r, err := oauth2login.LoginToServerManual(ctx, "https://kayra.example.com", os.Stdin, os.Stderr)
r, err := oauth2login.Refresh(ctx, "https://kayra.example.com", refreshToken)
err := oauth2login.Revoke(ctx, "https://kayra.example.com", refreshToken)
```

- **LoginToServer** listens on a loopback port, opens the browser at
  `authorization_endpoint` (PKCE S256, `state`, `resource` the server),
  where Kayra sends the human to its login provider. Kayra redirects back
  to the listener; the callback's `state` must be the login's and its
  `iss` the server's (RFC 9207), then the code is redeemed with this
  process's verifier: an access token, its `aud` the server, and a
  refresh token.
- **LoginToServerManual** (`--no-browser`) prints the URL to open on any
  machine; Kayra shows a code there, which this reads from stdin and
  redeems the same way. The code is redeemable with this process's
  verifier only.
- **Refresh** renews the tokens: Kayra rotates the refresh token. An
  `*Error` whose `Code` is `invalid_grant` means the login is revoked or
  expired.
- **Revoke** revokes a refresh token's whole login (`auth logout`).

Every command renews the context's access token when it expires within
30 seconds, and saves the new tokens (`resolveContext`).

## dev

```go
import "github.com/infrapi/cli/internal/dev"
```

`infrapi-cli dev`'s code, see [dev](dev.md) for the commands:

| | |
|---|---|
| `LoadConfig` | reads `dev.yaml`, refusing an unknown key, sets the defaults and checks it |
| `Stack` | a stack: its `dev.yaml`, found in the working directory or a parent, its directory, and how its containers run. `Up`, `Build`, `Compose` (down, clean, ps, logs...), `ResetKayra`, `Mint` and `Contexts`, `Health`, `Routes`, `Check`, `Doctor`. The containers go through `<engine> compose` with `os/exec`, the only way that behaves the same with podman and docker; what talks to Kayra or APISIX is Go |
| `Render` | renders `.infrapi-dev/` from `dev.yaml`: `env`, `kayra.yaml` (the embedded `templates/kayra.yaml.tmpl`, `kayra.config` merged in), `users.yaml`, `routes/*.json` (`gateway.routes` merged in, `routes/removed`), `app.env`, the stack (`templates/stack`: compose, APISIX, Kayra's Dockerfile), the signing keys (generated, or `kayra.keys`). Without `Force`, a render that would change a file writes nothing: `Rendered` holds the diff, the files, and `Keep`, the `dev.yaml` overrides that would give the files as they are |
| `Gateway` | an API's route through APISIX's Admin API |
| `WriteConfig`, `Ask` | a new `dev.yaml`, from the embedded `templates/dev.yaml.tmpl` and its answers: `dev init` without one, and `app init` |

## app

```go
import "github.com/infrapi/cli/internal/app"
```

`infrapi-cli app`'s code, see [app](app.md) for the commands. No dependency
of its own: `text/template`, `go/format`, `gopkg.in/yaml.v3`.

| | |
|---|---|
| `Init`, `Ask` | a new project from the embedded `templates/project` (`[[ ]]` delimiters, `_name_` in a path the API's name, a file rendering to nothing not written), then its `dev.yaml` and its generated code |
| `Add` | a resource's paths and schema inserted into the contract as text, its other lines kept; then `Generate`, and the operations' file and its test, written once |
| `Generate` | the contracts read (`readContracts`: `info.title`, `info.x-infrapi-group`, the `operationId`s), then `api/zz_generated.go` and `internal/<name>/zz_generated.go`, formatted by `go/format` |
| `Describe`, `FindRoot` | a project's API, as its contracts have it; the project a directory is in |
