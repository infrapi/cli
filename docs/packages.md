---
title: Packages
description: infrapi-cli's internal packages - clicontext, its saved logins, oauth2login, the browser-based login, app, the API project generator, and dev, the local dev stack.
weight: 50
---

# Packages

| Package | What it is |
|---|---|
| [`internal/clicontext`](#clicontext) | `~/.config/infrapi/config`: the saved logins, kubeconfig-style contexts |
| [`internal/oauth2login`](#oauth2login) | the CLI's half of the browser-based OAuth2 login, against Kayra's broker |
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
[`infrapi-cli login`](commands.md#login) writes to it on every successful
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

```go
import "github.com/infrapi/cli/internal/oauth2login"
```

Runs a real browser-based OAuth2 Authorization Code + PKCE flow against
a remote provider — the same flow `oc login` runs against OpenShift's
own `/oauth/authorize?client_id=...&code_challenge=...&redirect_uri=...`.
CLI-only ([`infrapi-cli login`](commands.md#login)); the API server never
runs this half — it only ever verifies tokens the provider already
issued, never requests them.

It never talks to the provider's own authorization endpoint directly.
Most enterprise IdPs — Okta included — will never allowlist a CLI's
own loopback redirect_uri (`http://127.0.0.1:PORT/callback`) for
security policy reasons, so the browser opens at **Kayra's own**
`/oauth2/v1/authorize` instead — the one address the provider's app
registration actually allowlists — and
[oauth2broker](https://infrapi.github.io/kayra/packages/oauth2broker/) relays the real flow back here
unchanged once it completes: Kayra never sees this CLI's own PKCE
verifier, and the final code→token exchange below still happens
directly against the provider, exactly as if there were no relay in
between.

### LoginToServer

```go
result, err := oauth2login.LoginToServer(ctx, serverBaseURL, os.Stderr)
```

- `serverBaseURL` (`--server`) is the *only* thing this is given — no
  provider name, no discovery URL, no client_id, and no local
  `kayra.yaml` to read any of that from. cmd/cli is designed to be
  handed to a plain end user who has none of it.
- It asks `serverBaseURL`'s own broker to pick a provider on its
  behalf (see [oauth2broker](https://infrapi.github.io/kayra/packages/oauth2broker/) —
  automatic when exactly one `remote.oauth2` provider is configured,
  or a human picks from a chooser page when there's more than one),
  and depends on the broker handing back that provider's own
  `token_endpoint`/`client_id` — public, non-secret OAuth2 client
  metadata, never anything Kayra would need to protect — in the
  loopback callback itself, just in time to finish the exchange
  directly against the real provider. An old broker (or one that
  somehow drops those two fields) fails loudly here rather than
  attempting an exchange with nothing to exchange against.
- `ctx` bounds the whole wait for the browser round trip — `login
  --timeout` controls this from the CLI.
- status lines go to the given writer (normally `os.Stderr`), including
  the URL to open in case the browser couldn't be launched
  automatically — many CLI sessions (SSH, containers) have no browser
  to open at all, so this is never the only way to proceed.

Kayra itself can never run this exchange on the CLI's behalf: the
`code_verifier` backing `code_challenge` never leaves this process, by
design (see [oauth2broker](https://infrapi.github.io/kayra/packages/oauth2broker/)) — only whoever holds it
can complete the exchange.

```go
type Result struct {
	AccessToken string
	TokenType   string
	Expiry      time.Time
}
```

`AccessToken` is what `infrapi-cli login` saves into the current
context ([clicontext](#clicontext)) — the provider's own token,
never a self-signed Kayra JWT, since there is no LDAP/local bind
here to embed anything from: the provider's own token/userinfo already
carries whatever a claims-backed `authorizations.<name>` entry needs
(see [config](https://infrapi.github.io/kayra/packages/config/)).

### LoginToServerManual

```go
result, err := oauth2login.LoginToServerManual(ctx, serverBaseURL, os.Stdin, os.Stderr)
```

`infrapi-cli login --no-browser`, for a terminal with no usable browser.
Same `Result`, same PKCE pair, same final exchange as `LoginToServer`,
but no loopback listener: it sends `redirect_uri=OOBRedirectURI`, prints
the authorize URL to stderr for the user to open on any machine, then
reads one line from stdin, the `LoginCode` the broker shows at the end
(`DecodeLoginCode`; surrounding whitespace ignored). A code whose
`state` isn't this attempt's own is refused before any exchange. The
exchange itself still goes from this process straight to the provider,
so the host must reach the provider's token endpoint. `ctx` bounds the
whole wait, prompt included.

`LoginCode`/`EncodeLoginCode`/`DecodeLoginCode` define that pasted string
(base64url JSON, no padding). Kayra's
[broker](https://infrapi.github.io/kayra/packages/oauth2broker/) encodes the
same fields with its own copy: keep the two in step.

### How the flow is driven

1. A local `127.0.0.1:0` listener claims an ephemeral port; its
   `/callback` path becomes this CLI's own loopback `redirect_uri` —
   never sent to the provider, only to Kayra's own broker.
2. A random `state` (CSRF) and a PKCE `code_verifier`
   (`oauth2.GenerateVerifier`) are generated; the browser opens at
   `{serverBaseURL}/oauth2/v1/authorize` carrying this CLI's own
   `redirect_uri`/`state`/`code_challenge` (never the verifier itself,
   and never a provider name — see [oauth2broker](https://infrapi.github.io/kayra/packages/oauth2broker/) for
   what happens to these, and how a provider gets resolved, on
   Kayra's side).
3. The call blocks on its own local callback, `ctx`, or a
   provider-reported `error` query param relayed back through the
   broker — whichever comes first. A `state` mismatch or missing
   `code` fails the whole call instead of silently proceeding.
4. A matching `state`, `code`, `token_endpoint` and `client_id` (the
   latter two relayed by the broker, not known ahead of time) drive the
   final exchange — directly against the provider's own
   `token_endpoint`, with `code_verifier` (`oauth2.VerifierOption`) —
   for the `Result` `LoginToServer` returns. `RedirectURL` for this
   call is `{serverBaseURL}/oauth2/v1/callback`, **not** this CLI's own
   loopback: the provider checks that string against whatever
   redirect_uri was actually used at its own `/authorize` (RFC 6749
   §4.1.3), and that was Kayra's callback, since Kayra — not this
   CLI — was the one that called it.

`OpenBrowser` is an exported, swappable var: both this package's own
tests and [oauth2broker](https://infrapi.github.io/kayra/packages/oauth2broker/)'s integration tests (which
exercise the real confirmation/chooser pages, not a stand-in) point it
at a double that drives the browser's role in the flow without an
actual display.

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
