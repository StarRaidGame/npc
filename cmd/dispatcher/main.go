// Command dispatcher is the StarRaid NPC dispatcher (see docs/npc.md): a worker
// tier that launches bot processes so the game server never hosts AI itself.
//
// This is the local-spawn build: it launches -bots persistent `npc` processes
// (each an ordinary client of the game server) and reports the live count on
// /stats for the control tools (stackctl). One process per NPC, fire-and-forget —
// the dispatcher tracks liveness for the count but does not restart bots; the
// server owns their lifecycle by the connection. The server-driven path
// (registering with the server, capacity heartbeats, spawning on the server's
// request over the TBD control API — docs/npc.md) layers on later; here the
// population is a local -bots knob.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func main() {
	server := flag.String("server", "localhost:60000", "game server address the bots connect to")
	n := flag.Int("bots", 2, "number of NPC bots to spawn")
	statsAddr := flag.String("stats", ":8091", "address for the /stats telemetry endpoint")
	user := flag.String("user", "dev", "bot login username (dev-stub auth)")
	secret := flag.String("secret", os.Getenv("STARRAID_DEV_SECRET"), "bot login secret (default $STARRAID_DEV_SECRET)")
	npcBin := flag.String("npc-bin", "", "prebuilt npc bot binary (default: go run ./cmd/npc)")
	flag.Parse()

	slog.Info("starraid npc dispatcher starting", "server", *server, "bots", *n, "stats", *statsAddr)
	if *secret == "" {
		slog.Warn("no bot secret (STARRAID_DEV_SECRET empty): bots will be rejected unless the server accepts these creds")
	}

	d := &dispatcher{server: *server, user: *user, secret: *secret, npcBin: *npcBin, bots: map[int]*botProc{}}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go d.serveStats(ctx, *statsAddr)
	for i := 0; i < *n; i++ {
		d.spawn(i)
	}

	<-ctx.Done()
	slog.Info("dispatcher stopping; retiring bots")
	d.shutdown()
}

type botProc struct {
	cmd  *exec.Cmd
	pgid int
}

type dispatcher struct {
	server, user, secret, npcBin string

	mu      sync.Mutex
	bots    map[int]*botProc
	spawned int
}

// spawn launches one persistent bot process in its own process group, tracks it,
// and reaps it (dropping the live count) when it exits. Fire-and-forget: no
// restart — the server owns the bot's lifecycle by the connection.
func (d *dispatcher) spawn(id int) {
	args := []string{"-persist", "-server", d.server, "-user", d.user, "-secret", d.secret}
	var cmd *exec.Cmd
	if d.npcBin != "" {
		cmd = exec.Command(d.npcBin, args...)
	} else {
		cmd = exec.Command("go", append([]string{"run", "./cmd/npc"}, args...)...)
	}
	cmd.Stderr = os.Stderr // surface bot login/connect errors; drop its verbose stdout
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		slog.Warn("bot spawn failed", "id", id, "err", err)
		return
	}
	pgid := cmd.Process.Pid
	d.mu.Lock()
	d.bots[id] = &botProc{cmd: cmd, pgid: pgid}
	d.spawned++
	d.mu.Unlock()
	slog.Info("bot spawned", "id", id, "pid", pgid)

	go func() {
		err := cmd.Wait()
		d.mu.Lock()
		delete(d.bots, id)
		live := len(d.bots)
		d.mu.Unlock()
		slog.Info("bot exited", "id", id, "err", err, "live", live)
	}()
}

// shutdown signals every live bot's process group (SIGTERM, then SIGKILL for
// stragglers) so the bots self-terminate.
func (d *dispatcher) shutdown() {
	signalAll := func(sig syscall.Signal) {
		d.mu.Lock()
		defer d.mu.Unlock()
		for _, b := range d.bots {
			if b.pgid > 1 {
				_ = syscall.Kill(-b.pgid, sig)
			}
		}
	}
	signalAll(syscall.SIGTERM)
	time.Sleep(2 * time.Second)
	signalAll(syscall.SIGKILL)
}

type stats struct {
	NpcsActive  int `json:"npcs_active"`
	NpcsSpawned int `json:"npcs_spawned"`
}

func (d *dispatcher) snapshot() stats {
	d.mu.Lock()
	defer d.mu.Unlock()
	return stats{NpcsActive: len(d.bots), NpcsSpawned: d.spawned}
}

func (d *dispatcher) serveStats(ctx context.Context, addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(d.snapshot())
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() { <-ctx.Done(); _ = srv.Shutdown(context.Background()) }()
	slog.Info("dispatcher stats listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("dispatcher stats server failed", "err", err)
	}
}
