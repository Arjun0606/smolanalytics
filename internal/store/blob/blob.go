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
	// write-temp-then-rename so a reader never sees a half-written object.
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
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
