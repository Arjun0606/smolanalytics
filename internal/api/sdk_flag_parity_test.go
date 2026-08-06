package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The JS flag evaluator must bucket IDENTICALLY to Go. Not closely — identically.
//
// A float that differs in the last ulp moves whoever sits on that boundary into the other arm.
// Nothing errors, nothing looks wrong, and the experiment quietly becomes a mixture of two
// populations whose difference is partly the change you shipped and partly which implementation
// happened to evaluate each user. Every result from it is unreadable, and there is no symptom to
// notice.
//
// So parity is asserted on the BITS, over the adversarial inputs, against the exact sdk.js bytes
// the server serves — not a copy, not a summary.
func TestLocalFlagBucketingMatchesGoBitForBit(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; the JS half of flag parity cannot be checked here")
	}

	// Inputs chosen to break a naive port rather than to pass:
	//   ""              — empty id
	//   "u:1" + "x:y:z" — a colon in BOTH salt and id, so a sloppy concatenation collides
	//   CJK / emoji     — multi-byte UTF-8; encoding per UTF-16 code unit diverges here
	//   surrogate pair  — the case a hand-written UTF-8 encoder gets wrong
	ids := []string{"", "u1", "u:1", "user-9999", "日本語テスト", "🎉emoji🎉", "a b c", "0", "-1", "uéaccent"}
	for i := 0; i < 300; i++ {
		ids = append(ids, fmt.Sprintf("visitor_%d", i))
	}
	salts := []string{"variant:banner", "rollout:banner", "layer:pricing", "holdout:g", "x:y:z"}

	type row struct{ Salt, ID, Bits string }
	var want []row
	for _, s := range salts {
		for _, id := range ids {
			want = append(want, row{s, id, fmt.Sprintf("%016x", math.Float64bits(goBucketForParity(s, id)))})
		}
	}
	in, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	fixture := filepath.Join(dir, "buckets.json")
	if err := os.WriteFile(fixture, in, 0o600); err != nil {
		t.Fatal(err)
	}
	sdkPath := filepath.Join(dir, "sdk.js")
	// The bytes the server actually serves. Copying the algorithm into the test instead would
	// prove only that the test agrees with itself.
	if err := os.WriteFile(sdkPath, []byte(sdkJS), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "run.js")
	os.WriteFile(script, []byte(`
const fs = require('fs');
const src = fs.readFileSync(process.argv[2], 'utf8');
// sdk.js is an IIFE over a browser global. Pull the two functions out by evaluating the file body
// in a scope that returns them, with the DOM touchpoints stubbed to nothing.
const shim = "var window={},document={referrer:'',addEventListener:function(){}},location={pathname:'/'},navigator={},localStorage={getItem:function(){return null},setItem:function(){},removeItem:function(){}};";
let expose;
try {
    // Run the file as the browser would, then read the evaluator off the global it installs.
  const w = new Function(shim + src + "\n; return window;")();
  expose = w.smolanalytics && w.smolanalytics.__flagInternals;
} catch (e) { console.log(JSON.stringify({error: 'sdk.js did not evaluate: ' + e.message})); process.exit(0); }
if (!expose || !expose.bucket) { console.log(JSON.stringify({error: '__flagInternals.bucket not found in the shipped sdk.js'})); process.exit(0); }
const rows = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
let bad = [];
for (const r of rows) {
  const v = expose.bucket(r.Salt, r.ID);
  const buf = Buffer.alloc(8); buf.writeDoubleBE(v);
  const bits = buf.toString('hex');
  if (bits !== r.Bits) bad.push({salt: r.Salt, id: r.ID, want: r.Bits, got: bits});
}
console.log(JSON.stringify({rows: rows.length, divergences: bad.length, first: bad.slice(0,3)}));
`), 0o600)

	out, err := exec.Command(node, script, sdkPath, fixture).CombinedOutput()
	if err != nil {
		t.Fatalf("node failed: %v\n%s", err, out)
	}
	var res struct {
		Rows        int                                    `json:"rows"`
		Divergences int                                    `json:"divergences"`
		Error       string                                 `json:"error"`
		First       []struct{ Salt, ID, Want, Got string } `json:"first"`
	}
	line := strings.TrimSpace(string(out))
	if i := strings.LastIndex(line, "\n"); i >= 0 {
		line = line[i+1:]
	}
	if err := json.Unmarshal([]byte(line), &res); err != nil {
		t.Fatalf("could not read the node result: %v\n%s", err, out)
	}
	if res.Error != "" {
		t.Fatalf("the JS evaluator could not be exercised: %s", res.Error)
	}
	if res.Rows != len(want) {
		t.Fatalf("checked %d rows, generated %d", res.Rows, len(want))
	}
	if res.Divergences > 0 {
		t.Fatalf("%d of %d buckets diverge between Go and the shipped sdk.js — an experiment split "+
			"across both would be reading two populations. First: %+v", res.Divergences, res.Rows, res.First)
	}
	t.Logf("%d bucket draws, bit-identical", res.Rows)
}

// goBucketForParity reproduces internal/flag.bucket, which is unexported.
//
// Duplicated deliberately and ONLY here: if the engine's construction ever changes, this test must
// fail rather than silently follow it, because the JS in the field will not have followed. The
// CurrentHashVersion constant is the coordinated way to change it, and the bundle ships that
// version so a client refuses what it cannot reproduce.
func goBucketForParity(salt, id string) float64 {
	sum := sha256.Sum256([]byte(salt + ":" + id))
	var u uint64
	for i := 0; i < 8; i++ {
		u = u<<8 | uint64(sum[i])
	}
	return float64(u>>4) / float64(uint64(1)<<60)
}
