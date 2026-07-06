// Command npc is the StarRaid reference bot — a headless client that connects
// over the same wire protocol as a human (see docs/npc.md). Fork it to automate
// your own account.
//
// Two modes: the default runs a short demo (handshake → login → one move → watch
// a few updates → exit); -persist keeps the session alive and wanders until the
// server drops the connection or the process is signalled — the shape the
// dispatcher spawns to populate the world with NPCs. The framing/codec is inlined
// (a shared client SDK across modules is a later concern); it mirrors
// server/internal/wire.
package main

import (
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/xuedi/starraid-protocol/gen/go/starraid/v1"
)

const maxFrameSize = 1 << 20 // must match the server's wire.MaxFrameSize

// wanderInterval is how often a persistent bot picks a new destination.
const wanderInterval = 6 * time.Second

func main() {
	server := flag.String("server", "localhost:60000", "game server address")
	user := flag.String("user", "dev", "login username")
	secret := flag.String("secret", "", "login secret")
	version := flag.Uint("version", 1, "protocol version to announce")
	persist := flag.Bool("persist", false, "stay connected and wander until disconnected (NPC mode)")
	flag.Parse()

	slog.Info("starraid npc (reference bot) starting", "server", *server, "persist", *persist)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *server, uint32(*version), *user, *secret, *persist); err != nil {
		slog.Error("npc session failed", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, addr string, version uint32, user, secret string, persist bool) error {
	if persist {
		return runPersistent(ctx, addr, version, user, secret)
	}
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	// A deadline covers only the handshake+login so a half-open server can't hang us.
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	if err := handshakeLogin(conn, version, user, secret); err != nil {
		return err
	}
	return demo(conn)
}

// runPersistent connects and wanders, retrying only the initial dial (the
// dispatcher tends to launch bots the moment the server starts, so it may not be
// listening yet). Once connected, an auth/protocol failure is terminal, and a
// mid-session disconnect means the server retired us — a clean exit either way.
func runPersistent(ctx context.Context, addr string, version uint32, user, secret string) error {
	for attempt := 0; attempt < 10; attempt++ {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Info("server unreachable; retrying", "attempt", attempt+1, "err", err)
			select {
			case <-time.After(1500 * time.Millisecond):
			case <-ctx.Done():
				return nil
			}
			continue
		}
		_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
		if err := handshakeLogin(conn, version, user, secret); err != nil {
			conn.Close()
			return err // auth/protocol failure is terminal — don't hammer the server
		}
		_ = conn.SetDeadline(time.Time{})
		err = wander(ctx, conn)
		conn.Close()
		return err
	}
	return fmt.Errorf("server unreachable after retries")
}

// handshakeLogin runs version negotiation then login, returning an error on any
// rejection or protocol surprise.
func handshakeLogin(conn net.Conn, version uint32, user, secret string) error {
	if err := writeClient(conn, &pb.ClientMessage{Msg: &pb.ClientMessage_Hello{
		Hello: &pb.Hello{ProtocolVersion: version},
	}}); err != nil {
		return fmt.Errorf("send Hello: %w", err)
	}
	vrMsg, err := readServer(conn)
	if err != nil {
		return fmt.Errorf("read VersionResult: %w", err)
	}
	vr := vrMsg.GetVersionResult()
	if vr == nil {
		return fmt.Errorf("expected VersionResult, got %T", vrMsg.Msg)
	}
	if !vr.Accepted {
		return fmt.Errorf("version %d rejected; server wants >= %d", version, vr.MinSupported)
	}

	if err := writeClient(conn, &pb.ClientMessage{Msg: &pb.ClientMessage_Login{
		Login: &pb.LoginRequest{Username: user, Secret: secret},
	}}); err != nil {
		return fmt.Errorf("send LoginRequest: %w", err)
	}
	lrMsg, err := readServer(conn)
	if err != nil {
		return fmt.Errorf("read LoginResult: %w", err)
	}
	lr := lrMsg.GetLoginResult()
	if lr == nil {
		return fmt.Errorf("expected LoginResult, got %T", lrMsg.Msg)
	}
	if !lr.Ok {
		return fmt.Errorf("login rejected: %s", lr.Reason)
	}
	slog.Info("authenticated", "user", user)
	return nil
}

// demo is the default short-lived behaviour: once assigned, issue one Move and
// watch our position advance a few times, then exit.
func demo(conn net.Conn) error {
	assigned, moveSent, selfUpdates := false, false, 0
	for {
		m, err := readServer(conn)
		if err != nil {
			return fmt.Errorf("read server message: %w", err)
		}
		switch {
		case m.GetSelfAssign() != nil:
			sa := m.GetSelfAssign()
			assigned = true
			slog.Info("self assigned", "object_id", sa.ObjectId, "x", sa.Position.GetX(), "y", sa.Position.GetY())
		case m.GetSelfUpdate() != nil:
			su := m.GetSelfUpdate()
			selfUpdates++
			slog.Info("self update", "object_id", su.ObjectId, "x", su.Position.GetX(), "y", su.Position.GetY())
			if assigned && !moveSent {
				if err := sendMove(conn, 5000, 3000); err != nil {
					return err
				}
				moveSent = true
			}
		}
		if moveSent && selfUpdates >= 6 {
			return nil
		}
	}
}

// wander is the persistent NPC loop: once assigned, keep picking new destinations
// on a ticker until the server drops the connection (the bot then self-terminates,
// per docs/npc.md) or the process is signalled. A reader goroutine drains inbound
// messages and reports disconnect; only this goroutine writes to the connection.
func wander(ctx context.Context, conn net.Conn) error {
	readErr := make(chan error, 1)
	assigned := make(chan struct{}, 1)
	go func() {
		seen := false
		for {
			m, err := readServer(conn)
			if err != nil {
				readErr <- err
				return
			}
			if !seen && m.GetSelfAssign() != nil {
				seen = true
				assigned <- struct{}{}
			}
		}
	}()

	select {
	case <-assigned:
	case err := <-readErr:
		return fmt.Errorf("disconnected before assignment: %w", err)
	case <-ctx.Done():
		return nil
	}

	if err := sendMove(conn, randCoord(), randCoord()); err != nil {
		return err
	}
	ticker := time.NewTicker(wanderInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil // signalled → exit cleanly
		case <-readErr:
			return nil // server dropped us → NPC self-terminates
		case <-ticker.C:
			if err := sendMove(conn, randCoord(), randCoord()); err != nil {
				return nil // write failed → treat as disconnect
			}
		}
	}
}

func sendMove(conn net.Conn, x, y int64) error {
	return writeClient(conn, &pb.ClientMessage{Msg: &pb.ClientMessage_Move{
		Move: &pb.Move{Target: &pb.Vec2{X: x, Y: y}},
	}})
}

// randCoord returns a wander destination in [-8000, 8000] (math/rand is
// auto-seeded per process, so each bot process wanders differently).
func randCoord() int64 { return int64(rand.Intn(16001) - 8000) }

// --- inline framing/codec (mirrors server/internal/wire) ---

func writeClient(w io.Writer, m *pb.ClientMessage) error {
	payload, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	if len(payload) > maxFrameSize {
		return errors.New("frame exceeds max size")
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

func readServer(r io.Reader) (*pb.ServerMessage, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxFrameSize {
		return nil, errors.New("frame exceeds max size")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	m := &pb.ServerMessage{}
	if err := proto.Unmarshal(buf, m); err != nil {
		return nil, err
	}
	return m, nil
}
