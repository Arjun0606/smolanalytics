// Package audit is the change log every real product needs — a record of the
// operator actions that mutate config or data (account changes, key create/revoke,
// retention, data clears). Append-only JSONL, bounded in memory, compacted on boot.
package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"time"
)

const maxEntries = 1000

// Entry is one logged action.
type Entry struct {
	Time   time.Time `json:"time"`
	Action string    `json:"action"`
	Detail string    `json:"detail"`
}

// Log is a concurrency-safe, persisted audit log.
type Log struct {
	mu      sync.Mutex
	path    string
	entries []Entry
	w       *os.File
}

// Open loads the log (keeping the most recent maxEntries) and compacts the file on
// boot so it can't grow without bound. Empty path = in-memory only.
func Open(path string) (*Log, error) {
	l := &Log{path: path}
	if path == "" {
		return l, nil
	}
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			var e Entry
			if json.Unmarshal(sc.Bytes(), &e) == nil {
				l.entries = append(l.entries, e)
			}
		}
		_ = f.Close()
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	compacted := false
	if len(l.entries) > maxEntries {
		l.entries = l.entries[len(l.entries)-maxEntries:]
		compacted = true
	}
	if compacted {
		var buf []byte
		for _, e := range l.entries {
			b, _ := json.Marshal(e)
			buf = append(buf, b...)
			buf = append(buf, '\n')
		}
		_ = os.WriteFile(path, buf, 0o600)
	}

	w, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	l.w = w
	return l, nil
}

// Record logs an action. Best-effort and never fatal — auditing must not break the
// operation it's recording.
func (l *Log) Record(action, detail string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := Entry{Time: time.Now().UTC(), Action: action, Detail: detail}
	l.entries = append(l.entries, e)
	if len(l.entries) > maxEntries {
		l.entries = l.entries[len(l.entries)-maxEntries:]
	}
	if l.w != nil {
		b, _ := json.Marshal(e)
		_, _ = l.w.Write(append(b, '\n'))
	}
}

// Recent returns the newest entries first (limit <= 0 means all held).
func (l *Log) Recent(limit int) []Entry {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(l.entries)
	if limit <= 0 || limit > n {
		limit = n
	}
	out := make([]Entry, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, l.entries[n-1-i])
	}
	return out
}

func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w == nil {
		return nil
	}
	err := l.w.Close()
	l.w = nil
	return err
}
