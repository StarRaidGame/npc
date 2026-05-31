// Command dispatcher is the StarRaid NPC dispatcher: it registers with the game
// server, reports spare capacity, and spawns bot processes on request, then
// fire-and-forgets them (see docs/npc.md).
package main

import (
	"flag"
	"log/slog"
)

func main() {
	server := flag.String("server", "localhost:8080", "game server control-API address")
	flag.Parse()

	slog.Info("starraid npc dispatcher starting", "server", *server)
	// TODO: register with the server, heartbeat capacity, handle spawn requests
	// by launching independent `npc` processes.
	slog.Info("dispatcher stub: nothing to do yet")
}
