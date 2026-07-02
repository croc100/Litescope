package dashboard

import "testing"

func TestBuildLockTimelineEmpty(t *testing.T) {
	tl := BuildLockTimeline("app.db", 0, nil)
	if tl.Events != 0 || tl.LockedEvents != 0 || len(tl.Windows) != 0 {
		t.Fatalf("empty timeline should be zero-valued, got %+v", tl)
	}
}

func TestBuildLockTimelineWindowsAndHolders(t *testing.T) {
	events := []LockEvent{
		{TS: 1000, State: "free", WALBytes: 10},
		{TS: 2000, State: "locked", WaitMS: 50, WALBytes: 20, Holders: []LockHolderInfo{{PID: 1, Command: "worker"}}},
		{TS: 3000, State: "locked", WaitMS: 120, WALBytes: 30, Holders: []LockHolderInfo{{PID: 2, Command: "worker"}}},
		{TS: 4000, State: "free", WALBytes: 30},
		{TS: 5000, State: "locked", WaitMS: 80, WALBytes: 40, Holders: []LockHolderInfo{{PID: 3, Command: "cron"}}},
		{TS: 6000, State: "free", WALBytes: 40},
	}
	tl := BuildLockTimeline("app.db", 0, events)

	if tl.Events != 6 {
		t.Errorf("Events = %d, want 6", tl.Events)
	}
	if tl.LockedEvents != 3 {
		t.Errorf("LockedEvents = %d, want 3", tl.LockedEvents)
	}
	if tl.MaxWaitMS != 120 {
		t.Errorf("MaxWaitMS = %d, want 120", tl.MaxWaitMS)
	}
	if len(tl.Windows) != 2 {
		t.Fatalf("Windows = %d, want 2", len(tl.Windows))
	}
	// First window spans the two consecutive locked events.
	w := tl.Windows[0]
	if w.StartTS != 2000 || w.EndTS != 3000 || w.DurationMS != 1000 {
		t.Errorf("window[0] span wrong: %+v", w)
	}
	if w.PeakWaitMS != 120 || w.Events != 2 {
		t.Errorf("window[0] stats wrong: %+v", w)
	}
	if len(w.Holders) != 1 || w.Holders[0] != "worker" {
		t.Errorf("window[0] holders = %v, want [worker]", w.Holders)
	}
	// worker held during 2 observations, cron during 1 — worker ranks first.
	if len(tl.TopHolders) != 2 || tl.TopHolders[0].Command != "worker" || tl.TopHolders[0].Events != 2 {
		t.Errorf("TopHolders = %+v", tl.TopHolders)
	}
	if tl.WALPeakBytes != 40 || tl.WALLastBytes != 40 {
		t.Errorf("WAL peak/last = %d/%d, want 40/40", tl.WALPeakBytes, tl.WALLastBytes)
	}
	if tl.WALBloated {
		t.Error("WALBloated should be false below threshold")
	}
}

func TestBuildLockTimelineWALBloat(t *testing.T) {
	events := []LockEvent{
		{TS: 1000, State: "free", WALBytes: walBloatBytes + 1},
	}
	tl := BuildLockTimeline("app.db", 0, events)
	if !tl.WALBloated {
		t.Error("WALBloated should be true past the threshold")
	}
}
