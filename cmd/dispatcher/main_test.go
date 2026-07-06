package main

import (
	"encoding/json"
	"testing"
)

func TestSnapshotCountsLiveAndSpawned(t *testing.T) {
	d := &dispatcher{bots: map[int]*botProc{}}
	if s := d.snapshot(); s.NpcsActive != 0 || s.NpcsSpawned != 0 {
		t.Fatalf("empty dispatcher: %+v", s)
	}

	d.bots[0], d.bots[1] = &botProc{pgid: 100}, &botProc{pgid: 200}
	d.spawned = 3 // one already exited
	if s := d.snapshot(); s.NpcsActive != 2 || s.NpcsSpawned != 3 {
		t.Fatalf("snapshot = %+v, want active=2 spawned=3", s)
	}

	// A reaped bot drops the active count but not the spawned total.
	delete(d.bots, 0)
	if s := d.snapshot(); s.NpcsActive != 1 || s.NpcsSpawned != 3 {
		t.Fatalf("after reap: %+v, want active=1 spawned=3", s)
	}
}

func TestStatsJSONFieldNames(t *testing.T) {
	// stackctl's telemetry client decodes these exact field names.
	b, err := json.Marshal(stats{NpcsActive: 2, NpcsSpawned: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"npcs_active":2,"npcs_spawned":5}`; got != want {
		t.Fatalf("json = %s, want %s", got, want)
	}
}
