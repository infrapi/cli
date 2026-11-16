---
title: Commands
description: Reference for infrapi-cli's commands against a Kayra server - login, context, auth, apis, and the get, create, update, patch and delete verbs.
weight: 30
---

# Commands

Every command here talks to a Kayra server's own API, through a saved
context (`login`, `context set`). The dev stack's commands are in
[dev](dev.md).

## Logging in

The portable, end-user client of a Kayra server — hand someone this
binary and a server URL, nothing else. It never reads `kayra.yaml`, has no `--config`
flag, and never needs to know which `authentifications.<name>` a server
configures: every command here only ever talks to that server's own API.

### login

A browser-based OAuth2 flow against a Kayra server you only know the URL
of — the one way to authenticate with this binary.

> [!TIP]
> The commands below use `infrapi-cli`: [install it](getting-started.md#1-install).

```bash
infrapi-cli login --server https://kayra.example.com
# oauth2login: opening browser to log in:
#
#   https://kayra.example.com/oauth2/v1/authorize?...
#
# infrapi-cli: logged in — saved context "kayra.infrapi.io" to ~/.config/infrapi/config (now current)
# infrapi-cli: run `infrapi-cli context token` to print the bearer token
```

The token itself is never printed — it's already in the context file this
just wrote; read it back with [`context token`](#context) whenever you
actually need it.

`--server`'s own `/oauth2/v1/authorize` resolves which
`authentifications.<name>` to use on your behalf — automatically when the
server configures exactly one `remote.oauth2` provider, or via a one-click
chooser page when it configures several (each option there links straight
to the provider's own login, never through a second confirmation click —
see [oauth2broker](https://infrapi.github.io/kayra/packages/oauth2broker/)) — and relays that provider's
own public token endpoint/client ID back at the end so this CLI can finish
the PKCE exchange itself, with zero prior knowledge of which provider it
was.

Nothing about the server but its URL is saved: what the commands need of
it, they discover — its check endpoint and own groups from
[`/.well-known/kayra.json`](https://infrapi.github.io/kayra/packages/discovery/), every API group's full
name and short name from `GET /apis`.

| Flag | Default | Meaning |
|------|---------|---------|
| `--server` | — | required; the Kayra server's base URL |
| `--context-name` | sanitized `--server` host | name to save this login under (see [context](#context)) |
| `--timeout` | `2m` (`10m` with `--no-browser`) | how long to wait for the browser round trip |
| `--no-browser` | off | log in from a terminal with no usable browser — see below |

#### Without a browser (SSH, container, air-gapped host)

`--no-browser` starts no local listener and opens nothing. It prints the
login URL; open it in a browser on **any** machine, log in as usual, and
the server shows a login code instead of redirecting back to
`127.0.0.1`. Paste that code at the prompt:

```bash
infrapi-cli login --server https://kayra.example.com --no-browser
# oauth2login: open this URL in a browser on any machine to log in:
#
#   https://kayra.example.com/oauth2/v1/authorize?...redirect_uri=urn%3Aietf%3Awg%3Aoauth%3A2.0%3Aoob...
#
# Paste the login code shown in the browser: eyJjb2RlIjoi...
# infrapi-cli: logged in — saved context "kayra.infrapi.io" to ~/.config/infrapi/config (now current)
```

The code is safe to carry across by hand: it is only redeemable together
with the PKCE verifier held by the terminal that printed the URL, and only
once, within the server's 10-minute login window. A code from any other
login attempt is refused. Paste it only into a terminal where you started
the login yourself.

The final code→token exchange still runs from this terminal straight to
the provider's token endpoint, so the terminal must be able to reach the
provider (Okta, say) — just not with a browser. Nothing changes in the
provider's app registration: it still only ever sees Kayra's own
`/oauth2/v1/callback`.

See [oauth2login](packages.md#oauth2login) for how the flow itself works.

### context

Manages `~/.config/infrapi/config` — the file `login` writes to on every
successful login ([internal/clicontext](packages.md#clicontext)). Its
shape mirrors `kubectl`'s own `~/.kube/config`: a map of named contexts
(server, token, expiry) plus a `current_context`, so switching back to an
earlier login never requires authenticating again. Override the path with
`$INFRAPI_CONTEXT_FILE`.

```bash
infrapi-cli login --server https://kayra.internal
infrapi-cli login --server https://kayra-staging.internal --context-name staging

infrapi-cli context list
# CURRENT  NAME                       SERVER                            EXPIRY
#          kayra.internal           https://kayra.internal          2026-11-03T16:04:05+01:00
# *        staging                    https://kayra-staging.internal  2026-11-03T17:04:05+01:00

# save an existing token (e.g. a `kayra token mint` bootstrap token) without logging in
infrapi-cli context set bootstrap --server http://localhost:8080 --token "$TOKEN"

infrapi-cli context use kayra.internal  # switch back without logging in again
infrapi-cli context current               # show the active context's details
infrapi-cli context token                 # print the active context's bearer token
infrapi-cli context token staging         # print a specific context's token
infrapi-cli context delete staging        # drop a saved login
```

| Subcommand | Args | Meaning |
|------------|------|---------|
| `list` | — | list saved contexts, marking the current one with `*` |
| `current` | — | print the current context's name, server, expiry |
| `set` | `<name>` | save `--token` for `--server` as a context and make it current — no login flow |
| `use` | `<name>` | switch the current context |
| `delete` | `<name>` | remove a saved context |
| `token` | `[name]` | print a context's bearer token (defaults to current) |

### auth (can-i, whoami)

Two checks that ask a server about the current login itself, rather
than managing it.

#### can-i

Mirrors `kubectl auth can-i`: asks the server's own
[check endpoint](https://infrapi.github.io/kayra/architecture/rbac-and-auth/#authorizing-other-apps-requests),
the one its [`/.well-known/kayra.json`](https://infrapi.github.io/kayra/packages/discovery/) names,
whether the current context's already-authenticated identity is
granted `<verb>` on `<resource>` for `--api` — the decision is always
the server's, never asserted locally.

```bash
infrapi-cli auth can-i get pods --api compute.infrapi.io --namespace core
# yes

infrapi-cli auth can-i delete pods --api compute.infrapi.io --namespace core
# no
# infrapi-cli: your roles in namespace "core" grant api "compute.infrapi.io", but not "delete" on "pods"
```

Prints `yes`/`no` to stdout and exits `0`/`1` accordingly — scriptable
exactly like `kubectl auth can-i`. A denial's reason (when the server
gives one) goes to stderr, never mixed into stdout.

**This only ever tests `rbac.Authorize`** — namespace-scoped
`Group`/`Role`/`RoleBinding` objects, the mechanism *other* apps built
on infrapi/lib use to authorize their own API. None of Kayra's own
management-plane routes are decided by it:

| Asking about... | Actually gated by |
|---|---|
| `authorization.<domain>`'s `groups`/`roles`/`rolebindings` | cluster RBAC (`CanManageNamespaceRBAC`) — a different, cluster-scoped `Group`/`Role`/`RoleBinding` set |
| `catalog.<domain>`'s `subscriptions` | cluster RBAC (`CanManageNamespaceRBAC`), same as above |
| `authorization.<domain>`'s `clustergroups`/`clusterroles`/`clusterrolebindings` | the sysadmin group alone (`SysadminGate`) — no `Rule` consulted at all |
| `catalog.<domain>`'s `apiregistrations` | `SysadminGate` alone |
| `system.<domain>`'s `namespaces` | `SysadminGate` alone |

Asking about one of these still gets a real, truthful answer — "does
a stored `Rule` grant this" — just not necessarily the answer to "will
the real endpoint let me." `can-i` prints a warning to stderr whenever
`--api`/the verb's resource names one of them, so a "yes" or "no"
here is never mistaken for the real decision. For anything
`SysadminGate`-gated, compare [`whoami`](#whoami)'s `groups` against
`sysadmin.groups` in the server's own config yourself — neither this
command nor the server exposes that comparison any other way.

| Flag | Default | Meaning |
|------|---------|---------|
| `--api` | — | required; the API group being asked about, by its full name (`compute.infrapi.io`) or short name (`compute`, resolved through `GET /apis`) — never Kayra's own `authorization.<domain>` |
| `--namespace` | — | required; the namespace to check against |
| `--context` | current context | saved context to use instead |

#### whoami

Prints exactly what the server's own `GET /oauth2/userinfo` returns
for the current login — `sub`, `provider`, `authorization`, `groups`,
plus whatever `authorizations.<name>` attributes (`email`, `name`,
...) that backend resolved. This is the same identity any `can-i`
decision, or any other RBAC check against this token, is actually made
against.

```bash
infrapi-cli auth whoami
# authorization:  idm
# email:           jdoe@example.com
# groups:          sandbox-core-sysadmin, sandbox-thetenant-admins
# name:            John Doe
# provider:        self
# sub:             jdoe
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--context` | current context | saved context to use instead |

### apis

Like `oc api-resources`: every api group the server knows and the resources
each one serves, from the server's `GET /apis` discovery. That covers
Kayra's own groups and every registered app, whose resources come from the
OpenAPI spec the reconciler fetched for it (see
[API registrations](https://infrapi.github.io/kayra/architecture/api-registrations/#openapi-spec)).

```bash
infrapi-cli apis
# NAME                SHORTNAME  APIGROUP                   NAMESPACED  VERBS
# namespaces          -          system.infrapi.io         false       create,delete,get,list,update
# apiregistrations    -          catalog.infrapi.io        false       create,delete,get,list,update
# subscriptions       -          catalog.infrapi.io        true        create,delete,get,list,update
# dashboards          telemetry  telemetry.infrapi.io    true        create,delete,get,list
# -                   -          vault.infrapi.io          -           -
infrapi-cli apis --api-group telemetry --namespaced=true
infrapi-cli apis -o yaml
```

A registered api whose spec hasn't been fetched yet (no sync so far) is
listed with `-`.

| Flag | Meaning |
|------|---------|
| `--api-group` | only this api group: its long name, short name, or first label (`telemetry`, `catalog`) |
| `--namespaced` | only namespaced (`true`) or cluster-scoped (`false`) resources |
| `-o`, `--output` | `table` (default), `json` or `yaml` |
| `--context` | saved context to use (defaults to the current one) |

## Calling the API (get, create, update, patch, delete)

kubectl-style top-level verbs — a thin, generic REST client against
*any* app's own API, following the exact route convention Kayra's
own resources use (see
[RBAC & auth](https://infrapi.github.io/kayra/architecture/rbac-and-auth/#authorizing-other-apps-requests)):

```
{server}/apis/<group>/v1/namespaces/<namespace>/<resource>[/<name>]   # -n given: namespace-scoped
{server}/apis/<group>/v1/<resource>[/<name>]                          # -n omitted: cluster-scoped
```

`<api>` is the group's full name (`vault.infrapi.io`), or its short name
or first label (`vault`, `catalog`), which `GET /apis` resolves to the one
group it names — as `kubectl` resolves a resource through discovery.

authenticated with the current context's own bearer token. Kayra
itself never implements any of these APIs (see
[API registrations](https://infrapi.github.io/kayra/architecture/api-registrations/)) — this is just
the one HTTP convention every app following it shares, so one verb set
works against all of them, Kayra's own resources included — both
namespace-scoped (`subscription`'s subscriptions, `authorization`'s
per-namespace groups/roles/rolebindings) and cluster-scoped
(`catalog`'s `apiregistrations`, `authorization`'s
`clustergroups`/`clusterroles`/`clusterrolebindings`):

Responses print as a table like `context list`'s: `NAMESPACE` (only with
`-n`), `NAME` (the resource's `name`, or `api` for a subscription),
`CREATED AT` and `UPDATED AT`, in local time. `-o json` or `-o yaml` prints
the full response instead (YAML keeps the server's field order). A response that isn't a named resource (a status
document, say) always prints as JSON, so nothing is hidden. An empty list
prints `No resources found.` on stderr and no rows on stdout. Errors print
on stderr as `Error: <title> (<status>): <detail>`, followed by
`Solution: …` when the server gives one, and the command exits `1`.

Each example's `curl` block is the exact request the verb sends, against
`http://localhost:8080` with `app.api_domain` left at `infrapi.io` —
take the token from the current context with
`TOKEN=$(infrapi-cli context token)`.

**infrapi-cli**

```bash
# namespace-scoped: -n selects .../namespaces/<ns>/<resource>
infrapi-cli get catalog -n thetenant subscriptions
# No resources found.
infrapi-cli create catalog -n thetenant subscriptions -d '{"api": "vault.infrapi.io"}'
# NAMESPACE  NAME               CREATED AT                 UPDATED AT
# thetenant  vault.infrapi.io  2026-11-10T14:30:00+01:00  2026-11-10T14:30:00+01:00
infrapi-cli get catalog -n thetenant subscriptions vault.infrapi.io -o json
# {"api": "vault.infrapi.io", "active": false, "state": "init", ...}
infrapi-cli update catalog -n thetenant subscriptions vault.infrapi.io/sync -d '{}'
infrapi-cli delete catalog -n thetenant subscriptions vault.infrapi.io
# subscriptions "vault.infrapi.io" deleted

# cluster-scoped: no -n, addresses .../<resource> directly
infrapi-cli create catalog apiregistrations -d '{"name": "vault.infrapi.io", "short_name": "vault", "owner": "sre"}'
infrapi-cli get catalog apiregistrations
infrapi-cli create authorization clustergroups -d '{"name": "g", "metadata": {...}, "groups": [...]}'
```

**curl**

```bash
# namespace-scoped: -n selects .../namespaces/<ns>/<resource>
curl -s http://localhost:8080/apis/catalog.infrapi.io/v1/namespaces/thetenant/subscriptions \
  -H "Authorization: Bearer $TOKEN"
# []
curl -s -X POST http://localhost:8080/apis/catalog.infrapi.io/v1/namespaces/thetenant/subscriptions \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"api": "vault.infrapi.io"}'
# {"api": "vault.infrapi.io", "active": false, "state": "init", ...}
curl -s http://localhost:8080/apis/catalog.infrapi.io/v1/namespaces/thetenant/subscriptions/vault.infrapi.io \
  -H "Authorization: Bearer $TOKEN"
curl -s -X PUT http://localhost:8080/apis/catalog.infrapi.io/v1/namespaces/thetenant/subscriptions/vault.infrapi.io/sync \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{}'
curl -s -X DELETE http://localhost:8080/apis/catalog.infrapi.io/v1/namespaces/thetenant/subscriptions/vault.infrapi.io \
  -H "Authorization: Bearer $TOKEN"

# cluster-scoped: no -n, addresses .../<resource> directly
curl -s -X POST http://localhost:8080/apis/catalog.infrapi.io/v1/apiregistrations \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"name": "vault.infrapi.io", "short_name": "vault", "owner": "sre"}'
curl -s http://localhost:8080/apis/catalog.infrapi.io/v1/apiregistrations \
  -H "Authorization: Bearer $TOKEN"
curl -s -X POST http://localhost:8080/apis/authorization.infrapi.io/v1/clustergroups \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"name": "g", "metadata": {...}, "groups": [...]}'
```

`get` maps to `GET` either way — no trailing `<name>` lists the whole
collection (`.../<resource>`), a `<name>` fetches that one item
(`.../<resource>/<name>`), exactly like `kubectl get` itself (and
exactly how Kayra's own collection vs. item routes already distinguish
the two) — there is no separate `list` verb. `create` is `POST`,
`update` is `PUT`, `patch` is `PATCH`, `delete` is `DELETE` —
`update`/`patch`/`delete` all require `<name>`; `create`/`update`/
`patch` all require a body. A sub-resource action is just part of
`<name>` (`vault/sync` above — "PATCH .../vm/:id/reboot" in the
general case, see
[RBAC & auth](https://infrapi.github.io/kayra/architecture/rbac-and-auth/#authorizing-other-apps-requests)),
never a separate argument.

The response body is printed pretty-printed as-is — this never
interprets or re-shapes it, since it has no idea what shape any given
`<api>`'s own resource actually is. A non-2xx response still prints
the body (typically the same RFC 9457 problem-detail shape
[`httpapi.Fail`](https://infrapi.github.io/kayra/packages/httpapi/) uses) and exits `1`.

| Flag | Default | Meaning |
|------|---------|---------|
| `--namespace`, `-n` | — | the namespace to address — omit for a cluster-scoped resource |
| `--data`, `-d` | — | request body, as a raw JSON string (`create`/`update`/`patch` only) |
| `--filename`, `-f` | — | request body, read from a file — `-` for stdin (`create`/`update`/`patch` only) |
| `--context` | current context | saved context to use instead |
