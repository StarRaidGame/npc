# npc

The StarRaid NPC subsystem (Go). See [../docs/npc.md](../docs/npc.md).

Two binaries:

- `cmd/npc` — the **reference bot**: a headless client that connects over the same wire
  [`protocol`](../protocol) as a human, logs in, claims a role, pulls a contract, and acts.
  Players are encouraged to fork it to automate their own account.
- `cmd/dispatcher` — the **dispatcher**: registers with the game server, reports spare
  capacity, and spawns bot processes on request (fire-and-forget), then leaves them to talk
  to the server directly.

## Prerequisites

- Go 1.26+
- A local checkout of the [`protocol`](../protocol) repo (its generated Go bindings are
  committed, so no `protoc` is needed just to build)

## Getting started

```sh
just install                                # wire the workspace to protocol + fetch deps
just run -server localhost:60000 -user dev -secret s3cr3t   # connect + handshake + login
just run-dispatcher                         # run the dispatcher instead
```

`just install` writes a local `go.work` that points at the `protocol` repo. By default it
looks for a sibling checkout (`../protocol`); if yours lives elsewhere, set the path:

```sh
just protocol_path=/path/to/protocol install
# or persistently:
export STARRAID_PROTOCOL_PATH=/path/to/protocol
```

`go.work` is gitignored — it is per-developer local config, never committed. Run `just` to
list every recipe (`build`, `run`, `run-dispatcher`, `test`, `fmt`, `vet`, `clean`).

The server controls a bot's lifetime by the connection — close it and the process exits.
