---
title: infrapi-cli
description: InfraPI's command line - log in to Kayra and call its API, and scaffold and run a local dev stack.
weight: 10
---

# infrapi-cli

InfraPI's command line, one binary for two things:

- **Kayra's client.** Log in to a Kayra server you only know the URL of,
  keep the login as a context, ask whether you may act, list the APIs, and
  call them: `login`, `context`, `auth`, `apis`, `get`, `create`,
  `update`, `patch`, `delete`. It never reads a server's configuration:
  everything goes through the server's own API.
- **The dev stack.** Scaffold a local InfraPI (Kayra, etcd and APISIX on
  podman or docker) to develop an API against, and run it: `dev init`,
  `dev up`, `dev contexts`, `dev logs`, `dev down`...

| I want to... | Go to |
|---|---|
| Install it, log in, make a first call | [Getting started](getting-started.md) |
| Know every command against Kayra, and its flags | [Commands](commands.md) |
| Create a dev stack, and run it | [dev](dev.md) |
| Read the code: contexts, the login flow, the dev stack | [Packages](packages.md) |

Examples of calls to Kayra are given twice: with `infrapi-cli`, then with
`curl`.
