# infrapi-cli

InfraPI's command line, one binary for two things:

- **Kayra's client**: log in to a Kayra server you only know the URL of,
  keep the login as a context, and call its API (`login`, `context`,
  `auth can-i`, `apis`, `get`, `create`, `update`, `patch`, `delete`).
- **The dev stack**: scaffold a local InfraPI to develop an API against
  (`dev init`), and run it on podman or docker (`dev up`, `dev contexts`,
  `dev logs`, `dev down`...), no `make` needed.

```bash
go install github.com/infrapi/cli/cmd/infrapi-cli@latest

infrapi-cli login --server https://kayra.example.com
infrapi-cli apis

infrapi-cli dev init compute && cd compute && infrapi-cli dev up
```

## Docs

| Page | What it covers |
|---|---|
| [Getting started](docs/getting-started.md) | Install, log in, a first call |
| [Commands](docs/commands.md) | Every command against Kayra, and its flags |
| [dev](docs/dev.md) | `dev init`, `up`, `contexts`, and every command running the stack |
| [Packages](docs/packages.md) | `clicontext`, `oauth2login`, `dev` |

## Layout

```text
cmd/infrapi-cli/        the commands
internal/clicontext/    ~/.config/infrapi/config: saved logins
internal/oauth2login/   the browser-based login, against Kayra's broker
internal/dev/           dev.yaml, render, the stack (compose), apply, the gateway routes, init (scaffold)
docs/                   the docs, published on the InfraPI portal
```

## License

MIT, see [LICENSE](LICENSE).
