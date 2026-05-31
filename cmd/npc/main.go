// Command npc is the StarRaid reference bot — a headless client that connects
// over the same wire protocol as a human (see docs/npc.md). Fork it to automate
// your own account.
package main

import (
	"flag"
	"log/slog"
)

func main() {
	server := flag.String("server", "localhost:60000", "game server address")
	flag.Parse()

	slog.Info("starraid npc (reference bot) starting", "server", *server)
	// TODO: connect, version handshake, login, claim a role, pull a contract, act.
	slog.Info("npc stub: nothing to do yet")
}
