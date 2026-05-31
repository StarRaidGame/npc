// Command npc is the StarRaid reference bot — a headless client that connects
// over the same wire protocol as a human (see docs/npc.md). Fork it to automate
// your own account.
//
// For now it exercises the first server slice: version handshake + login. The
// framing/codec is inlined (a shared client SDK across modules is a later
// concern); it mirrors server/internal/wire.
package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/xuedi/starraid-protocol/gen/go/starraid/v1"
)

const maxFrameSize = 1 << 20 // must match the server's wire.MaxFrameSize

func main() {
	server := flag.String("server", "localhost:60000", "game server address")
	user := flag.String("user", "dev", "login username")
	secret := flag.String("secret", "", "login secret")
	version := flag.Uint("version", 1, "protocol version to announce")
	flag.Parse()

	slog.Info("starraid npc (reference bot) starting", "server", *server)
	if err := run(*server, uint32(*version), *user, *secret); err != nil {
		slog.Error("npc session failed", "err", err)
		os.Exit(1)
	}
}

func run(addr string, version uint32, user, secret string) error {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))

	// 1) Version handshake.
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
	slog.Info("version accepted", "protocol_version", version)

	// 2) Login.
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

	// 3) Spawn / SelfAssign: the server tells us which object we control and its
	// initial state (see docs/protocol.md "Session lifecycle").
	saMsg, err := readServer(conn)
	if err != nil {
		return fmt.Errorf("read SelfAssign: %w", err)
	}
	sa := saMsg.GetSelfAssign()
	if sa == nil {
		return fmt.Errorf("expected SelfAssign, got %T", saMsg.Msg)
	}
	slog.Info("self assigned", "object_id", sa.ObjectId, "x", sa.Position.GetX(), "y", sa.Position.GetY())

	suMsg, err := readServer(conn)
	if err != nil {
		return fmt.Errorf("read SelfUpdate: %w", err)
	}
	su := suMsg.GetSelfUpdate()
	if su == nil {
		return fmt.Errorf("expected SelfUpdate, got %T", suMsg.Msg)
	}
	slog.Info("self update", "object_id", su.ObjectId, "x", su.Position.GetX(), "y", su.Position.GetY())

	// 4) Movement: ask to move toward a point and watch the position advance.
	target := &pb.Vec2{X: 5000, Y: 3000}
	if err := writeClient(conn, &pb.ClientMessage{Msg: &pb.ClientMessage_Move{
		Move: &pb.Move{Target: target},
	}}); err != nil {
		return fmt.Errorf("send Move: %w", err)
	}
	slog.Info("moving", "target_x", target.X, "target_y", target.Y)
	for i := 0; i < 5; i++ {
		m, err := readServer(conn)
		if err != nil {
			return fmt.Errorf("read SelfUpdate: %w", err)
		}
		if u := m.GetSelfUpdate(); u != nil {
			slog.Info("position", "object_id", u.ObjectId, "x", u.Position.GetX(), "y", u.Position.GetY())
		}
	}

	// TODO: claim a role, pull a contract, act (later slices).
	return nil
}

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
