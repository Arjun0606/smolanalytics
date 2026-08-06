package flag

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/Arjun0606/smolanalytics/internal/query"
)

// A definitions bundle: enough for a client or edge worker to evaluate flags locally, with no
// network round-trip per check.
//
// PUBLISHING A DEFINITION PUBLISHES ITS TARGETING VALUES. A rule reading
// `plan in ["enterprise","legacy_pro"]` or `email regex @acme\.com$` becomes readable by anyone who
// views source. That is not a leak of user data, but it is a leak of commercial intent — which
// customers are on a legacy plan, which domain is being courted — and it reverses a promise this
// codebase makes explicitly, that definitions never leave the server.
//
// So it is per-flag opt-in, default off, and the opt-in is the ENTIRE authorization model. Not a
// key scope, not a heuristic about which rules look sensitive: a human deciding, flag by flag,
// that this one's rules are safe to publish.

// bundleVersion is the wire schema. A client that does not understand it must refuse the bundle
// and fall back to server evaluation rather than guess at fields it has never seen.
const bundleVersion = 1

// Bundle is the published payload. Single-letter keys: this ships to every page on every load, and
// the field names would otherwise outweigh the data.
type Bundle struct {
	V    int          `json:"v"`
	HV   int          `json:"hv"` // hash generation — a client that cannot reproduce it must not try
	ETag string       `json:"etag"`
	TTL  int          `json:"ttl"`
	Rest bool         `json:"rest"` // enabled flags exist OUTSIDE this bundle; still ask the server for those
	F    []BundleFlag `json:"f"`
}

type BundleFlag struct {
	K  string          `json:"k"`            // the key — LOAD-BEARING, it is the bucketing salt
	M  int             `json:"m,omitempty"`  // measured → the client must log an exposure
	V  []BundleVariant `json:"v,omitempty"`  // ORDER PRESERVED: variant bucketing walks it in order
	L  string          `json:"l,omitempty"`  // layer
	S  []float64       `json:"s,omitempty"`  // layer slice [start,end)
	H  string          `json:"h,omitempty"`  // holdout
	HP float64         `json:"hp,omitempty"` // holdout percent
	R  []BundleRule    `json:"r,omitempty"`  // ORDER PRESERVED: first match wins
}

type BundleVariant struct {
	K string `json:"k"`
	W int    `json:"w"`
}

type BundleRule struct {
	P int            `json:"p"`           // rollout percent
	F []BundleFilter `json:"f,omitempty"` // all must pass
}

type BundleFilter struct {
	P string `json:"p"`
	O string `json:"o"`
	V any    `json:"v,omitempty"`
	// N is the comparand PRE-PARSED as Go parsed it, for numeric ops only.
	//
	// This exists to delete a whole class of divergence rather than test for it. Go's
	// strconv.ParseFloat and JavaScript's Number() disagree on real inputs — "1_000", "0x10",
	// leading/trailing space, "" — and each disagreement silently moves one user into a different
	// arm. Shipping the number Go already computed, including its zero fallback for a
	// non-numeric comparand, means the client never parses it at all.
	N *float64 `json:"n,omitempty"`
}

// BuildBundle publishes every flag that is BOTH enabled and explicitly opted in.
//
// Rest reports whether other enabled flags exist. Without it a client that evaluated the bundle
// locally would conclude the flags it did not find are off — silently disabling every
// server-evaluated flag on the page.
func BuildBundle(flags []Flag, ttlSeconds int) Bundle {
	b := Bundle{V: bundleVersion, HV: CurrentHashVersion, TTL: ttlSeconds}
	sort.Slice(flags, func(i, j int) bool { return flags[i].Key < flags[j].Key })
	for _, f := range flags {
		if !f.Enabled {
			continue // a disabled flag is omitted entirely; publishing "off" leaks it exists
		}
		if !f.Local {
			b.Rest = true
			continue
		}
		bf := BundleFlag{K: f.Key}
		if f.Measured {
			bf.M = 1
		}
		for _, v := range f.Variants {
			bf.V = append(bf.V, BundleVariant{K: v.Key, W: v.Weight})
		}
		if e := f.Experiment; e != nil {
			bf.L, bf.H, bf.HP = e.Layer, e.Holdout, e.HoldoutPct
			if !e.Slice.Empty() {
				bf.S = []float64{e.Slice.Start, e.Slice.End}
			}
		}
		for _, r := range f.Rules {
			br := BundleRule{P: r.RolloutPct}
			for _, fl := range r.Filters {
				bfl := BundleFilter{P: fl.Property, O: string(fl.Op), V: fl.Value}
				if fl.Op == query.Gt || fl.Op == query.Lt {
					n := query.ToNum(fl.Value)
					bfl.N = &n
				}
				br.F = append(br.F, bfl)
			}
			bf.R = append(bf.R, br)
		}
		b.F = append(b.F, bf)
	}
	b.ETag = bundleETag(b)
	return b
}

// bundleETag fingerprints the DEFINITIONS, not the response.
//
// Computed over the flag list with TTL and the etag field itself excluded, so an unchanged set of
// flags yields a stable tag across restarts and across replicas. Hashing the whole marshalled
// response instead would change the tag whenever anything incidental moved, and every client would
// re-download a bundle that had not changed.
func bundleETag(b Bundle) string {
	stable := struct {
		V  int          `json:"v"`
		HV int          `json:"hv"`
		F  []BundleFlag `json:"f"`
	}{V: b.V, HV: b.HV, F: b.F}
	raw, err := json.Marshal(stable)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}
