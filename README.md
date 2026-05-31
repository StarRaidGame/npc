# npc

The StarRaid NPC subsystem (Go). See [../docs/npc.md](../docs/npc.md).

Two binaries:

- `cmd/npc` — the **reference bot**: a headless client that connects over the same wire
  [`protocol`](../protocol) as a human, logs in, claims a role, pulls a contract, and acts.
  Players are encouraged to fork it to automate their own account.
- `cmd/dispatcher` — the **dispatcher**: registers with the game server, reports spare
  capacity, and spawns bot processes on request (fire-and-forget), then leaves them to talk
  to the server directly.

```sh
go run ./cmd/npc           # or: just run-npc
go run ./cmd/dispatcher    # or: just run-dispatcher
```

The server controls a bot's lifetime by the connection — close it and the process exits.
