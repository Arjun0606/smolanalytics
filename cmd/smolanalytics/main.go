// Command smolanalytics is the single binary: product analytics you can run with
// one command, no cluster. `demo` seeds a populated dashboard in 60 seconds;
// `serve` persists events to a durable log; `mcp` lets your own AI read it.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/alert"
	alias2 "github.com/Arjun0606/smolanalytics/internal/alias"
	"github.com/Arjun0606/smolanalytics/internal/api"
	"github.com/Arjun0606/smolanalytics/internal/audit"
	"github.com/Arjun0606/smolanalytics/internal/cohort"
	"github.com/Arjun0606/smolanalytics/internal/defined"
	"github.com/Arjun0606/smolanalytics/internal/demo"
	"github.com/Arjun0606/smolanalytics/internal/deploys"
	"github.com/Arjun0606/smolanalytics/internal/exportlink"
	"github.com/Arjun0606/smolanalytics/internal/flag"
	"github.com/Arjun0606/smolanalytics/internal/geo"
	"github.com/Arjun0606/smolanalytics/internal/goal"
	"github.com/Arjun0606/smolanalytics/internal/gsc"
	"github.com/Arjun0606/smolanalytics/internal/insight"
	"github.com/Arjun0606/smolanalytics/internal/insights"
	"github.com/Arjun0606/smolanalytics/internal/mcp"
	"github.com/Arjun0606/smolanalytics/internal/settings"
	"github.com/Arjun0606/smolanalytics/internal/share"
	"github.com/Arjun0606/smolanalytics/internal/store"
	"github.com/Arjun0606/smolanalytics/internal/store/blob"
	"github.com/Arjun0606/smolanalytics/internal/store/file"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
	"github.com/Arjun0606/smolanalytics/internal/store/segment"
	"github.com/Arjun0606/smolanalytics/internal/trackplan"
	"github.com/Arjun0606/smolanalytics/internal/webhook"
)

func main() {
	cmd := ""
	if len(os.Args) >= 2 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		st, closeStore, err := openServeStore()
		if err != nil {
			log.Fatal(err)
		}
		serve(st, closeStore, true) // real data — guard against public bind without auth
	case "demo":
		// Live() keeps the dataset anchored to today and "Live now" populated for as long
		// as the process runs — a hosted demo that seeds once at boot goes stale within a
		// day and starts fabricating a "pageviews dropped 100%" verdict.
		st, err := demo.Live()
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("smolanalytics: seeded demo data (in-memory, self-refreshing, not persisted)")
		serve(st, func() error { return nil }, false) // throwaway demo data — safe to expose
	case "mcp":
		// `mcp --host=<cloud> --key=<key>` runs the cloud-aware proxy (instrument tools
		// read the local repo, everything else forwards to the cloud instance). Plain
		// `mcp` serves the local data file.
		if h, k := parseHostKey(os.Args[2:]); h != "" && k != "" {
			runMCPProxy(h, k)
		} else {
			runMCP()
		}
	case "connect":
		connect(os.Args[2:])
	case "gsc":
		gscCmd(os.Args[2:])
	case "import":
		importCmd(os.Args[2:])
	case "instrument", "wizard":
		instrumentCmd(os.Args[2:])
	case "brief":
		briefCmd(os.Args[2:])
	case "deploy":
		deployCmd(os.Args[2:])
	case "plan":
		planCmd(os.Args[2:])
	case "scrub":
		// verify the cold tier's invariants (every segment readable, CRC-clean, counts
		// match) and clean up orphaned blobs left by failed deletes. Exit 1 on problems.
		b, label, err := coldBlob()
		if err != nil {
			log.Fatal(err)
		}
		if b == nil {
			log.Fatal("scrub: no cold tier configured (set SMOLANALYTICS_COLD or SMOLANALYTICS_S3_BUCKET)")
		}
		st, err := segment.Open(dataPath(), b, envInt("SMOLANALYTICS_SEAL_EVENTS"))
		if err != nil {
			log.Fatal(err)
		}
		report, deleted := st.Scrub()
		fmt.Printf("scrub of %s\n", label)
		fmt.Printf("  segments: %d   events: %d (+%d hot)\n", report.Segments, report.Events, report.HotEvents)
		fmt.Printf("  orphaned blobs removed: %d\n", deleted)
		if len(report.Problems) > 0 {
			for _, p := range report.Problems {
				fmt.Printf("  PROBLEM: %s\n", p)
			}
			os.Exit(1)
		}
		fmt.Println("  all invariants hold ✓")
	default:
		fmt.Println("smolanalytics — product analytics in one binary")
		fmt.Println()
		fmt.Println("  smolanalytics demo    seed a realistic dataset + open a populated dashboard")
		fmt.Println("  smolanalytics serve   persist events from POST /v1/events to a durable log")
		fmt.Println("  smolanalytics mcp     MCP server over stdio — connect your Claude/Cursor and ask anything")
		fmt.Println("  smolanalytics connect wire it into your coding assistant (Claude Desktop/Code, Cursor,")
		fmt.Println("                        Windsurf, VS Code, Cline) in one command, then ask")
		fmt.Println("  smolanalytics scrub   verify the cold tier (every segment readable, CRC-clean,")
		fmt.Println("                        counts match) and remove orphaned blobs")
		fmt.Println("  smolanalytics gsc     connect Google Search Console (auth / status)")
		fmt.Println("  smolanalytics brief   print the morning digest: pulse + what to look at")
		fmt.Println("                        (--json, --webhook=URL for Slack, --days=7 — cron it)")
		fmt.Println("  smolanalytics deploy  record a deploy marker (git HEAD by default) so you can ask")
		fmt.Println("                        \"did that ship move the metric?\" — one line in CI")
		fmt.Println("  smolanalytics plan    tracking plan as a file in your repo — init/push/pull/check;")
		fmt.Println("                        `plan check` exits 1 when tracking silently broke (CI gate)")
		fmt.Println()
		fmt.Println("  ADDR                      listen address (default 127.0.0.1:8080 = local only;")
		fmt.Println("                            set 0.0.0.0:8080 to expose — then a password is required)")
		fmt.Println("  SMOLANALYTICS_PASSWORD    dashboard login; required to `serve` on a public interface")
		fmt.Println("  SMOLANALYTICS_WRITE_KEY   PUBLIC ingest key — gates POST /v1/events only (ships in the SDK snippet)")
		fmt.Println("  SMOLANALYTICS_READ_KEY    SECRET read key — gates GET /v1 reports, /v1/export, and MCP")
		fmt.Println("  SMOLANALYTICS_DB          event log path (default ./smolanalytics.data)")
		fmt.Println("  SMOLANALYTICS_RETAIN_DAYS drop events older than N days (default: keep forever)")
		fmt.Println("  SMOLANALYTICS_MAX_EVENTS  keep only the newest N events resident (memory guardrail)")
		fmt.Println("  SMOLANALYTICS_COLD        dir for the scale tier: columnar segments, bounded RAM,")
		fmt.Println("                            history to billions of events (default: single-file log)")
		fmt.Println("  SMOLANALYTICS_S3_BUCKET   cold segments on S3/R2/Tigris instead of a local dir")
		fmt.Println("                            (+_ENDPOINT _REGION _ACCESS_KEY _SECRET_KEY _PREFIX)")
		fmt.Println("  SMOLANALYTICS_SEAL_EVENTS events per columnar segment when COLD/S3 is set (default 50k)")
		fmt.Println("  the running server also speaks MCP at POST /mcp (Streamable HTTP)")
	}
}

func dataPath() string {
	if p := os.Getenv("SMOLANALYTICS_DB"); p != "" {
		return p
	}
	return "smolanalytics.data"
}

// openServeStore picks the storage backend. Default: the durable single-file log (one
// box, everything-resident, capped by SMOLANALYTICS_MAX_EVENTS). Set SMOLANALYTICS_COLD
// to a directory (or, later, an object-store URL) to switch on the scale tier: a bounded
// hot log that seals into compressed columnar segments, so memory stays flat while
// history grows to billions of events for pennies. Same interface either way.
func openServeStore() (store.Store, func() error, error) {
	if b, label, err := coldBlob(); err != nil {
		return nil, nil, err
	} else if b != nil {
		s, err := segment.Open(dataPath(), b, envInt("SMOLANALYTICS_SEAL_EVENTS"))
		if err != nil {
			return nil, nil, err
		}
		log.Printf("smolanalytics: scale backend — hot log %s + columnar segments on %s (%d events)", dataPath(), label, s.Count())
		return s, s.Close, nil
	}

	fs, err := file.Open(dataPath())
	if err != nil {
		return nil, nil, err
	}
	log.Printf("smolanalytics: %d events loaded from %s", fs.Count(), dataPath())
	if n := envInt("SMOLANALYTICS_MAX_EVENTS"); n > 0 {
		if err := fs.SetMaxEvents(n); err != nil {
			return nil, nil, err
		}
		log.Printf("smolanalytics: memory cap — keeping the newest %d events resident", n)
	}
	return fs, fs.Close, nil
}

// coldBlob builds the cold-tier object-storage backend from env, or (nil,"",nil) if none
// is configured (then we fall back to the single-file log). S3 wins over a local dir.
func coldBlob() (blob.Blob, string, error) {
	if bucket := os.Getenv("SMOLANALYTICS_S3_BUCKET"); bucket != "" {
		b, err := blob.NewS3(
			os.Getenv("SMOLANALYTICS_S3_ENDPOINT"),
			os.Getenv("SMOLANALYTICS_S3_REGION"),
			bucket,
			os.Getenv("SMOLANALYTICS_S3_ACCESS_KEY"),
			os.Getenv("SMOLANALYTICS_S3_SECRET_KEY"),
			os.Getenv("SMOLANALYTICS_S3_PREFIX"),
		)
		if err != nil {
			return nil, "", err
		}
		return b, "s3://" + bucket, nil
	}
	if dir := os.Getenv("SMOLANALYTICS_COLD"); dir != "" {
		b, err := blob.NewLocal(dir)
		if err != nil {
			return nil, "", err
		}
		return b, dir, nil
	}
	return nil, "", nil
}

// envInt reads a non-negative integer env var; returns 0 if unset or unparseable.
func envInt(key string) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// displayURL renders a clickable URL from an ADDR. ADDR can be ":8080" (no host),
// "127.0.0.1:7799", or "0.0.0.0:8080" — normalize each to something a human can open.
func displayURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://localhost" + addr // addr was ":8080"-ish; best effort
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// isLoopbackBind reports whether ADDR binds only the loopback interface (so it's not
// reachable from another machine). An empty host or 0.0.0.0/:: is a wildcard = public.
func isLoopbackBind(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return host == "localhost"
}

func serve(st store.Store, closeStore func() error, guardPublic bool) {
	demoMode := !guardPublic
	// demo must be a zero-footprint experience: sidecar stores live in memory,
	// nothing is written to the visitor's working directory.
	sp := func(suffix string) string {
		if demoMode {
			return ""
		}
		return dataPath() + suffix
	}
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080" // local-only by default; set ADDR=0.0.0.0:8080 to expose (needs a password)
	}
	// identity stitching: wrap the store so every read canonicalizes ids through the
	// alias map (and GDPR erasure fans out across a user's anonymous trail).
	var aliasMap *alias2.Map
	if am, err := alias2.Open(sp(".aliases.json")); err == nil {
		aliasMap = am
		st = alias2.Wrap(st, am)
	} else {
		log.Printf("smolanalytics: identity stitching disabled (%v)", err)
	}
	// retroactive defined events (the Heap wedge): resolve named events from autocapture
	// at read time, so a PM can turn captured clicks into "checkout" with zero code.
	var definedStore *defined.Store
	if ds, err := defined.Open(sp(".defined.json")); err == nil {
		definedStore = ds
		st = defined.Wrap(st, ds)
	} else {
		log.Printf("smolanalytics: defined events disabled (%v)", err)
	}
	app := api.New(st)
	if aliasMap != nil {
		app.SetAliases(aliasMap)
	}
	if definedStore != nil {
		app.SetDefined(definedStore)
	}
	app.SetWriteKey(os.Getenv("SMOLANALYTICS_WRITE_KEY")) // PUBLIC: ingest only (embedded in the SDK)
	app.SetReadKey(os.Getenv("SMOLANALYTICS_READ_KEY"))   // SECRET: reads + MCP (never ship in client code)
	if os.Getenv("SMOLANALYTICS_GEO") != "off" {
		// country resolution from the free DB-IP lite db (CC BY 4.0) — downloads on
		// first boot, loads in the background, never blocks serving. IPs never stored.
		app.SetGeo(geo.Open(sp(".geoip.csv.gz")))
	}
	if ins, err := insights.Open(sp(".insights.json")); err == nil {
		app.SetInsights(ins)
	} else {
		log.Printf("smolanalytics: saved reports disabled (%v)", err)
	}
	if coh, err := cohort.Open(sp(".cohorts.json")); err == nil {
		app.SetCohorts(coh)
	} else {
		log.Printf("smolanalytics: cohorts disabled (%v)", err)
	}
	hasAccount := false // an in-app dashboard account counts as auth too, not just the env password
	if set, err := settings.Open(sp(".settings.json")); err == nil {
		// Default retention from env (the cloud sets this per plan) — only if the
		// operator hasn't already chosen one in the dashboard, which persists and wins.
		if d := envInt("SMOLANALYTICS_RETAIN_DAYS"); d > 0 && set.RetainDays() == 0 {
			if err := set.SetRetainDays(d); err == nil {
				log.Printf("smolanalytics: retention — keeping %d days of events", d)
			}
		}
		app.SetSettings(set)
		hasAccount = set.HasPassword()
		go pruneLoop(st, set)
	} else {
		log.Printf("smolanalytics: settings persistence disabled (%v)", err)
	}
	if al, err := audit.Open(sp(".audit.jsonl")); err == nil {
		app.SetAudit(al)
	} else {
		log.Printf("smolanalytics: audit log disabled (%v)", err)
	}
	if wh, err := webhook.Open(sp(".webhooks.json")); err == nil {
		app.SetWebhooks(wh)
		go dailyBrief(st, wh)
	} else {
		log.Printf("smolanalytics: webhooks disabled (%v)", err)
	}
	if al, err := alert.Open(sp(".alerts.json")); err == nil {
		app.SetAlerts(al)
		go alertLoop(app)
	} else {
		log.Printf("smolanalytics: alerts disabled (%v)", err)
	}
	if tp, err := trackplan.Open(sp(".trackplan.json")); err == nil {
		app.SetTrackPlan(tp)
	} else {
		log.Printf("smolanalytics: tracking plan disabled (%v)", err)
	}
	if gl, err := goal.Open(sp(".goals.json")); err == nil {
		app.SetGoals(gl)
		if demoMode {
			_, _ = gl.Save(goal.Definition{Name: "Signed up", Kind: "event", Value: "signup"})
			_, _ = gl.Save(goal.Definition{Name: "Paid", Kind: "event", Value: "checkout"})
		}
	} else {
		log.Printf("smolanalytics: goals disabled (%v)", err)
	}
	if sh, err := share.Open(sp(".shares.json")); err == nil {
		app.SetShares(sh)
	} else {
		log.Printf("smolanalytics: share links disabled (%v)", err)
	}
	if dp, err := deploys.Open(sp(".deploys.json")); err == nil {
		app.SetDeploys(dp)
	} else {
		log.Printf("smolanalytics: deploys disabled (%v)", err)
	}
	if fs, err := flag.Open(sp(".flags.json")); err == nil {
		app.SetFlags(fs)
	} else {
		log.Printf("smolanalytics: feature flags disabled (%v)", err)
	}
	if ex, err := exportlink.Open(sp(".exportlinks.json")); err == nil {
		app.SetExportLinks(ex)
	} else {
		log.Printf("smolanalytics: export links disabled (%v)", err)
	}
	if gs, err := gsc.Open(sp(".gsc.json")); err == nil {
		app.SetGSC(gs)
		if demoMode {
			// the demo shows every card populated — including search + money pages
			_ = demo.SeedGSC(gs)
		}
		if creds, ok := gsc.CredsFromEnv(); ok && gs.Connected() {
			go func() { // pull now if stale, then every 12h
				for {
					ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
					if err := gs.Poll(ctx, creds); err != nil {
						log.Printf("smolanalytics: gsc poll failed (%v)", err)
					}
					cancel()
					time.Sleep(12 * time.Hour)
				}
			}()
		}
	} else {
		log.Printf("smolanalytics: search console disabled (%v)", err)
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Graceful shutdown so the event log is flushed and closed cleanly.
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = closeStore()
	}()

	authOn := os.Getenv("SMOLANALYTICS_PASSWORD") != "" || hasAccount
	if !authOn {
		// Safe by default: never let REAL data be served unauthenticated on a public
		// interface by accident. Localhost is fine (unreachable from outside); demo data
		// is fine (throwaway). A public bind with real data and no password is refused.
		if guardPublic && !isLoopbackBind(addr) && os.Getenv("SMOLANALYTICS_ALLOW_UNAUTHENTICATED") == "" {
			log.Fatalf("smolanalytics: refusing to serve real data on %s without a password —\n"+
				"  this binds a public interface and the dashboard, exports and MCP would be open to anyone.\n"+
				"  do ONE of:\n"+
				"    • set SMOLANALYTICS_PASSWORD=...            (recommended for anything internet-facing)\n"+
				"    • ADDR=127.0.0.1:8080                       (local only — this is the default)\n"+
				"    • SMOLANALYTICS_ALLOW_UNAUTHENTICATED=1     (only on a trusted private network)", addr)
		}
		log.Printf("smolanalytics: no password set — dashboard/exports/MCP are unauthenticated (ok on %s)", displayURL(addr))
	}
	log.Printf("smolanalytics: dashboard on %s · MCP at %s/mcp", displayURL(addr), displayURL(addr))
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// pruneLoop enforces the retention policy: on boot and every 6h, delete events
// older than the configured retain-days window (0 = keep forever).
func pruneLoop(st store.Store, set *settings.Store) {
	prune := func() {
		days := set.RetainDays()
		if days <= 0 {
			return
		}
		before := time.Now().UTC().AddDate(0, 0, -days)
		if n, err := st.Prune(before); err != nil {
			log.Printf("smolanalytics: retention prune failed: %v", err)
		} else if n > 0 {
			log.Printf("smolanalytics: retention pruned %d events older than %d days", n, days)
		}
	}
	prune()
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for range t.C {
		prune()
	}
}

// dailyBrief delivers the proactive "what to look at" verdict to configured
// webhooks once a day — the morning brief, the habit loop.
func dailyBrief(st store.Store, wh *webhook.Store) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for range t.C {
		evs, err := st.Range(time.Time{}, time.Time{})
		if err != nil {
			continue
		}
		findings := insight.Generate(evs)
		if len(findings) == 0 {
			continue
		}
		text := insight.Text(findings)
		wh.DeliverAll(map[string]any{
			"type":     "daily_brief",
			"text":     text,
			"findings": findings,
			"at":       time.Now().UTC(),
		}, text)
	}
}

// alertLoop evaluates alerts on boot and every 5 minutes, firing webhooks for any
// whose condition is met.
func alertLoop(app *api.Server) {
	app.EvaluateAlerts()
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		app.EvaluateAlerts()
	}
}

// runMCP serves the analytics over MCP on stdio for a local Claude Desktop / Cursor,
// reading the SAME durable log the server persists to. If that log is empty, it
// falls back to throwaway demo data (in-memory) so a first-time connect still works
// without writing demo events into the real file.
func runMCP() {
	fs, err := file.Open(dataPath())
	if err != nil {
		log.Fatal(err)
	}
	var st store.Store = fs
	if fs.Count() == 0 {
		_ = fs.Close()
		m := memory.New()
		if err := demo.Seed(m); err != nil {
			log.Fatal(err)
		}
		st = m
	}
	var aliasMap *alias2.Map
	if am, err := alias2.Open(dataPath() + ".aliases.json"); err == nil {
		aliasMap = am
		st = alias2.Wrap(st, am)
	}
	m := mcp.New(st)
	if aliasMap != nil {
		m.SetAliases(aliasMap) // import_events stitches $identify like the server would
	}
	// export links stay unwired on stdio ON PURPOSE: a download URL only works when
	// the HTTP server that will serve (and burn) the token minted it — a stdio-minted
	// token would be a dead link. The tool's error points at connecting over HTTP.
	// action tools (create_alert, save_report, …) write the same sidecar files the
	// server uses, so anything created from the editor shows up on the dashboard.
	if ins, err := insights.Open(dataPath() + ".insights.json"); err == nil {
		m.SetInsights(ins)
	}
	if coh, err := cohort.Open(dataPath() + ".cohorts.json"); err == nil {
		m.SetCohorts(coh)
	}
	if wh, err := webhook.Open(dataPath() + ".webhooks.json"); err == nil {
		m.SetWebhooks(wh)
	}
	if al, err := alert.Open(dataPath() + ".alerts.json"); err == nil {
		m.SetAlerts(al)
	}
	if set, err := settings.Open(dataPath() + ".settings.json"); err == nil {
		m.SetSettings(set)
	}
	if tp, err := trackplan.Open(dataPath() + ".trackplan.json"); err == nil {
		m.SetTrackPlan(tp)
	}
	if gl, err := goal.Open(dataPath() + ".goals.json"); err == nil {
		m.SetGoals(gl)
	}
	if sh, err := share.Open(dataPath() + ".shares.json"); err == nil {
		m.SetShares(sh)
	}
	if dp, err := deploys.Open(dataPath() + ".deploys.json"); err == nil {
		m.SetDeploys(dp)
	}
	if fs, err := flag.Open(dataPath() + ".flags.json"); err == nil {
		m.SetFlags(fs)
	}
	if gs, err := gsc.Open(dataPath() + ".gsc.json"); err == nil {
		m.SetGSC(gs)
	}
	if err := m.ServeStdio(); err != nil {
		log.Fatal(err)
	}
}
