# infrapi-cli

InfraPI's command line, one binary for three things:

- **Kayra's client**: log in to a Kayra server you only know the URL of,
  keep the login as a context, and call its API (`auth login`,
  `auth context`, `auth can-i`, `apis`, `get`, `create`, `update`, `patch`,
  `delete`).
- **Your API**: create a Go project on infrapi/lib, add resources to its
  OpenAPI contract, generate its code, register it in Kayra (`app init`,
  `app add`, `app generate`, `app register`).
- **The dev stack**: a local InfraPI to develop the API against, on podman
  or docker (`dev init`, `dev up`, `dev contexts`, `dev logs`,
  `dev down`...), no `make` needed.

```bash
bin install github.com/infrapi/cli ~/.local/bin/infrapi-cli   # or: go install github.com/infrapi/cli/cmd/infrapi-cli@latest

infrapi-cli auth login --server https://kayra.example.com
infrapi-cli apis

infrapi-cli app init compute && cd compute
infrapi-cli dev init && infrapi-cli dev up
```

## Docs

| Page | What it covers |
|---|---|
| [Getting started](docs/getting-started.md) | Install, log in, a first call |
| [Commands](docs/commands.md) | Every command against Kayra, and its flags |
| [app](docs/app.md) | `app init`, `add`, `generate`, `register` |
| [dev](docs/dev.md) | `dev init`, `up`, `contexts`, and every command running the stack |
| [Develop](docs/develop/index.md) | Getting started, Develop an API, The dev stack |
| [Packages](docs/packages.md) | `clicontext`, `oauth2login`, `app`, `dev` |

## Layout

```text
cmd/infrapi-cli/        the commands
internal/clicontext/    ~/.config/infrapi/config: saved logins
internal/oauth2login/   the browser-based login, against Kayra's broker
internal/app/           infrapi-cli app: the project's templates, the contract, the generator
internal/dev/           dev.yaml, render, the stack (compose), the gateway routes, init
docs/                   the docs, published on the InfraPI portal
```

## License

MIT, see [LICENSE](LICENSE).
