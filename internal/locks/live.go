package locks

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// LockState is the live locking status of a database at one instant.
type LockState string

const (
	StateFree     LockState = "free"     // a write lock can be acquired right now
	StateLocked   LockState = "locked"   // a writer currently holds the lock (SQLITE_BUSY)
	StateReadable LockState = "readable" // reads succeed but the write lock is contended
	StateError    LockState = "error"    // the database could not be probed
)

// Holder is a process holding the database file open, as reported by lsof.
type Holder struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
	Access  string `json:"access"` // "r" | "w" | "rw" | "u" (unknown)
}

// LiveProbe is a point-in-time snapshot of who holds the lock and whether a
// writer can proceed. Unlike Diagnose (which reads static PRAGMA config), this
// observes the database as it is right now.
type LiveProbe struct {
	Source    string    `json:"source"`
	Time      time.Time `json:"time"`
	State     LockState `json:"state"`
	Detail    string    `json:"detail"`
	WaitMS    int64     `json:"wait_ms"`           // how long BEGIN IMMEDIATE waited before resolving
	Holders   []Holder  `json:"holders,omitempty"` // processes with the file open (local only)
	Hint      string    `json:"hint,omitempty"`    // remediation hint when contended
	WALExists bool      `json:"wal_exists"`
	WALBytes  int64     `json:"wal_bytes"` // WAL file size at probe time (0 if none)
}

// Probe observes the live lock state of a local SQLite database. It is
// read-only and side-effect-free: it opens a fresh connection, attempts to
// acquire the write lock with a short timeout, and immediately rolls back.
//
// Remote sources (d1://, turso://) have no observable file-level lock, so Probe
// returns an error directing the caller to the static Diagnose path.
func Probe(path string, waitTimeout time.Duration) (*LiveProbe, error) {
	if strings.HasPrefix(path, "d1://") || strings.HasPrefix(path, "turso://") {
		return nil, fmt.Errorf("live lock probe is only available for local SQLite files; use Diagnose for remote sources")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("cannot open %s: %w", path, err)
	}

	walBytes := walFileSize(path)
	p := &LiveProbe{
		Source:    path,
		Time:      time.Now(),
		WALExists: walBytes > 0,
		WALBytes:  walBytes,
	}

	// busy_timeout governs how long BEGIN IMMEDIATE waits for the write lock
	// before returning SQLITE_BUSY. A short timeout lets us observe contention
	// without blocking the caller for long.
	ms := int64(waitTimeout / time.Millisecond)
	if ms < 0 {
		ms = 0
	}
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(%d)", path, ms)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		p.State, p.Detail = StateError, err.Error()
		return p, nil
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	// Pin a single connection so BEGIN IMMEDIATE and ROLLBACK run on the same
	// session — the database/sql pool would otherwise spread them across conns.
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		p.State, p.Detail = StateError, err.Error()
		p.Holders = fileHolders(path)
		return p, nil
	}
	defer conn.Close()

	// BEGIN IMMEDIATE acquires a RESERVED (write) lock immediately, unlike the
	// default DEFERRED transaction which only locks on first write. busy_timeout
	// governs how long it waits before returning SQLITE_BUSY.
	start := time.Now()
	_, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE")
	p.WaitMS = time.Since(start).Milliseconds()
	if err != nil {
		if isBusy(err) {
			p.State = StateLocked
			p.Detail = "another connection holds the write lock (SQLITE_BUSY on BEGIN IMMEDIATE)"
			p.Holders = fileHolders(path)
			p.Hint = lockedHint(p.Holders)
			return p, nil
		}
		p.State, p.Detail = StateError, err.Error()
		p.Holders = fileHolders(path)
		return p, nil
	}
	_, _ = conn.ExecContext(ctx, "ROLLBACK")

	p.State = StateFree
	p.Detail = "write lock is available — no writer is currently holding it"
	p.Holders = fileHolders(path)
	return p, nil
}

// Watch repeatedly probes the database and calls onChange whenever the lock
// state changes (and once at the start). It blocks until the context-less stop
// channel is closed.
func Watch(path string, interval, waitTimeout time.Duration, stop <-chan struct{}, onChange func(*LiveProbe)) error {
	if strings.HasPrefix(path, "d1://") || strings.HasPrefix(path, "turso://") {
		return fmt.Errorf("live lock watch is only available for local SQLite files")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var last LockState = ""
	emit := func() {
		p, err := Probe(path, waitTimeout)
		if err != nil {
			return
		}
		if p.State != last {
			last = p.State
			onChange(p)
		}
	}
	emit() // initial reading
	for {
		select {
		case <-stop:
			return nil
		case <-ticker.C:
			emit()
		}
	}
}

// isBusy reports whether an error is a SQLITE_BUSY / "database is locked" error.
func isBusy(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "database is locked") ||
		strings.Contains(s, "sqlite_busy") ||
		strings.Contains(s, "(5)") // SQLITE_BUSY primary result code
}

func lockedHint(holders []Holder) string {
	if len(holders) == 0 {
		return "Set busy_timeout on every connection so writers retry instead of failing: PRAGMA busy_timeout=5000;"
	}
	var pids []string
	for _, h := range holders {
		pids = append(pids, fmt.Sprintf("%s(%d)", h.Command, h.PID))
	}
	return fmt.Sprintf("Holders: %s. Ensure they use WAL mode and a busy_timeout, and keep write transactions short.",
		strings.Join(pids, ", "))
}

// fileHolders lists processes holding the database file (or its -wal/-shm
// sidecars) open, via lsof. Best-effort: returns nil if lsof is unavailable
// (e.g. Windows) or finds nothing.
func fileHolders(path string) []Holder {
	if runtime.GOOS == "windows" {
		return nil
	}
	lsof, err := exec.LookPath("lsof")
	if err != nil {
		return nil
	}
	self := os.Getpid()
	seen := map[int]*Holder{}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		out, err := exec.Command(lsof, "-F", "pcafn", path+suffix).Output()
		if err != nil {
			continue // lsof exits non-zero when no process holds the file
		}
		parseLsof(out, self, seen)
	}
	if len(seen) == 0 {
		return nil
	}
	holders := make([]Holder, 0, len(seen))
	for _, h := range seen {
		holders = append(holders, *h)
	}
	return holders
}

// parseLsof parses lsof -F field output, accumulating one Holder per PID.
// Fields: p<pid>, c<command>, a<access mode: r/w/u>, f<fd>, n<name>.
func parseLsof(out []byte, self int, seen map[int]*Holder) {
	var cur *Holder
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		tag, val := line[0], line[1:]
		switch tag {
		case 'p':
			pid, err := strconv.Atoi(val)
			if err != nil || pid == self {
				cur = nil
				continue
			}
			if h, ok := seen[pid]; ok {
				cur = h
			} else {
				h := &Holder{PID: pid, Access: "u"}
				seen[pid] = h
				cur = h
			}
		case 'c':
			if cur != nil {
				cur.Command = val
			}
		case 'a':
			if cur != nil && val != "" {
				cur.Access = normalizeAccess(val)
			}
		}
	}
}

func normalizeAccess(a string) string {
	switch a {
	case "r":
		return "r"
	case "w":
		return "w"
	case "u":
		return "rw"
	default:
		return a
	}
}
