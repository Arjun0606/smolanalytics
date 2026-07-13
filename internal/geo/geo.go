// Package geo resolves visitor IPs to a country code using the DB-IP Lite free
// database (db-ip.com, CC BY 4.0 — the dashboard credits it wherever countries
// render). Chosen over MaxMind because the download needs no account or key.
//
// Design constraints, in order: (1) privacy — the IP is used for one in-memory
// lookup at ingest and never stored; only the ISO country code is stamped on the
// event. (2) zero dependencies — the CSV edition parses with the stdlib into a
// sorted range table (~600k IPv4 ranges ≈ 12MB resident), no mmdb reader needed.
// (3) never block serving — Open returns immediately and loads (downloading the
// ~10MB file on first boot) in the background; lookups before readiness return ""
// and events simply carry no country, exactly like a backend event would.
package geo

import (
	"compress/gzip"
	"encoding/binary"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"
)

type rng struct {
	start, end uint32
	cc         string
}

type Resolver struct {
	mu sync.RWMutex
	v4 []rng // sorted by start
}

// Open returns a resolver immediately and loads path in the background. If the
// file is missing it is downloaded from DB-IP first (month-stamped URL, falling
// back to the previous month around release day). Any failure logs once and
// leaves the resolver empty — geo is an enrichment, never a dependency.
func Open(path string) *Resolver {
	r := &Resolver{}
	go func() {
		if _, err := os.Stat(path); err != nil {
			if err := download(path); err != nil {
				log.Printf("smolanalytics: geo disabled (%v) — set SMOLANALYTICS_GEO=off to silence", err)
				return
			}
		}
		if err := r.load(path); err != nil {
			log.Printf("smolanalytics: geo db unreadable (%v) — delete %s to re-download", err, path)
		}
	}()
	return r
}

// Country returns the ISO 3166-1 alpha-2 code for ip, or "" when unknown,
// non-IPv4, or the table isn't loaded yet.
func (r *Resolver) Country(ip net.IP) string {
	if r == nil || ip == nil {
		return ""
	}
	v4 := ip.To4()
	if v4 == nil {
		return "" // IPv6 ranges are in the same file; deliberately deferred
	}
	n := binary.BigEndian.Uint32(v4)
	r.mu.RLock()
	defer r.mu.RUnlock()
	i := sort.Search(len(r.v4), func(i int) bool { return r.v4[i].end >= n })
	if i < len(r.v4) && r.v4[i].start <= n {
		return r.v4[i].cc
	}
	return ""
}

func (r *Resolver) load(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	cr := csv.NewReader(gz)
	cr.FieldsPerRecord = 3
	var v4 []rng
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		start := net.ParseIP(rec[0]).To4()
		end := net.ParseIP(rec[1]).To4()
		if start == nil || end == nil {
			continue // IPv6 rows
		}
		v4 = append(v4, rng{
			start: binary.BigEndian.Uint32(start),
			end:   binary.BigEndian.Uint32(end),
			cc:    rec[2],
		})
	}
	sort.Slice(v4, func(i, j int) bool { return v4[i].start < v4[j].start })
	r.mu.Lock()
	r.v4 = v4
	r.mu.Unlock()
	log.Printf("smolanalytics: geo ready (%d IPv4 ranges, db-ip.com CC BY 4.0)", len(v4))
	return nil
}

func download(path string) error {
	// month-stamped releases; on the 1st the current month may not exist yet
	now := time.Now().UTC()
	urls := []string{
		fmt.Sprintf("https://download.db-ip.com/free/dbip-country-lite-%s.csv.gz", now.Format("2006-01")),
		fmt.Sprintf("https://download.db-ip.com/free/dbip-country-lite-%s.csv.gz", now.AddDate(0, -1, 0).Format("2006-01")),
	}
	var lastErr error
	for _, u := range urls {
		if err := fetchTo(path, u); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

func fetchTo(path, url string) error {
	cl := &http.Client{Timeout: 2 * time.Minute}
	resp, err := cl.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
