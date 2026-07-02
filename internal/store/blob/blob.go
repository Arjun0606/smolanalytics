// Package blob is object storage as one small interface: whole-object Put/Get/List/
// Delete. The Local backend backs self-host and dev; an S3/R2/Tigris backend (same
// interface) backs cloud cold storage. This single seam is the entire object-storage
// story — the segment store never knows which backend it's talking to.
package blob

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Blob interface {
	Put(key string, data []byte) error
	Get(key string) ([]byte, error) // returns os.ErrNotExist semantics when absent
	List(prefix string) ([]string, error)
	Delete(key string) error
}

// Local stores objects as files under a directory. Keys map to relative paths; any
// "../" traversal is rejected so a key can't escape the root.
type Local struct{ dir string }

func NewLocal(dir string) (*Local, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Local{dir: dir}, nil
}

func (l *Local) path(key string) (string, error) {
	clean := filepath.Clean("/" + key) // force-absolute then strip, kills ".."
	return filepath.Join(l.dir, clean), nil
}

func (l *Local) Put(key string, data []byte) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	// write-temp → fsync → rename → fsync dir: a reader never sees a half-written
	// object AND a power cut can't leave a renamed-but-empty segment (rename alone
	// doesn't force the data to disk — the same discipline the hot log uses).
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(p)); err == nil {
		_ = d.Sync() // make the rename itself durable; best-effort (not all FSes support it)
		_ = d.Close()
	}
	return nil
}

func (l *Local) Get(key string) ([]byte, error) {
	p, err := l.path(key)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

func (l *Local) Delete(key string) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (l *Local) List(prefix string) ([]string, error) {
	var keys []string
	err := filepath.WalkDir(l.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, rerr := filepath.Rel(l.dir, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if strings.HasSuffix(rel, ".tmp") {
			return nil
		}
		if strings.HasPrefix(rel, prefix) {
			keys = append(keys, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(keys)
	return keys, nil
}
