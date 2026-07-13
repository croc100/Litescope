package dashboard

import "testing"

func lockedEvt(ts, wait int64, holder string) LockEvent {
	e := LockEvent{TS: ts, State: "locked", WaitMS: wait}
	if holder != "" {
		e.Holders = []LockHolderInfo{{PID: 1, Command: holder}}
	}
	return e
}

func TestBuildFleetLockReport_RanksWorstFirst(t *testing.T) {
	entries := []FleetLockEntry{
		{Name: "quiet", Source: "quiet.db", Events: []LockEvent{
			{TS: 1000, State: "free"},
		}},
		{Name: "contended", Source: "contended.db", Events: []LockEvent{
			lockedEvt(1000, 50, "worker"),
			lockedEvt(1500, 80, "worker"),
			{TS: 2000, State: "free"},
		}},
		{Name: "critical", Source: "crit.db", Events: []LockEvent{
			// A single window spanning 40s → longest jam ≥ 30s = critical.
			lockedEvt(1000, 200, "batch"),
			lockedEvt(41000, 200, "batch"),
		}},
	}

	r := BuildFleetLockReport(0, entries)
	if got := len(r.Databases); got != 3 {
		t.Fatalf("want 3 databases, got %d", got)
	}
	if r.Databases[0].Name != "critical" || r.Databases[0].Severity != "critical" {
		t.Errorf("want critical first, got %+v", r.Databases[0])
	}
	if r.Databases[1].Name != "contended" || r.Databases[1].Severity != "warning" {
		t.Errorf("want contended second (warning), got %+v", r.Databases[1])
	}
	if r.Databases[2].Name != "quiet" || r.Databases[2].Severity != "ok" {
		t.Errorf("want quiet last (ok), got %+v", r.Databases[2])
	}

	ok, warning, critical := r.Counts()
	if ok != 1 || warning != 1 || critical != 1 {
		t.Errorf("counts: want 1/1/1, got %d/%d/%d", ok, warning, critical)
	}
	if !r.HasContention() {
		t.Error("HasContention should be true")
	}
}

func TestBuildFleetLockReport_WALBloatIsCritical(t *testing.T) {
	entries := []FleetLockEntry{
		{Name: "bloated", Source: "b.db", Events: []LockEvent{
			{TS: 1000, State: "free", WALBytes: walBloatBytes + 1},
		}},
	}
	r := BuildFleetLockReport(0, entries)
	if r.Databases[0].Severity != "critical" {
		t.Errorf("WAL bloat should be critical, got %q", r.Databases[0].Severity)
	}
	if !r.Databases[0].WALBloated {
		t.Error("WALBloated flag should be set")
	}
}

func TestBuildFleetLockReport_NoEventsIsOK(t *testing.T) {
	entries := []FleetLockEntry{{Name: "empty", Source: "e.db"}}
	r := BuildFleetLockReport(0, entries)
	if r.Databases[0].Severity != "ok" {
		t.Errorf("no events should be ok, got %q", r.Databases[0].Severity)
	}
	if r.HasContention() {
		t.Error("empty fleet should have no contention")
	}
}
