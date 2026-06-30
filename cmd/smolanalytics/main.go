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
	"github.com/Arjun0606/smolanalytics/internal/api"
	"github.com/Arjun0606/smolanalytics/internal/audit"
	"github.com/Arjun0606/smolanalytics/internal/cohort"
	"github.com/Arjun0606/smolanalytics/internal/demo"
	"github.com/Arjun0606/smolanalytics/internal/insight"
	"github.com/Arjun0606/smolanalytics/internal/insights"
	"github.com/Arjun0606/smolanalytics/internal/mcp"
	"github.com/Arjun0606/smolanalytics/internal/settings"
	"github.com/Arjun0606/smolanalytics/internal/store"
	"github.com/Arjun0606/smolanalytics/internal/store/blob"
	"github.com/Arjun0606/smolanalytics/internal/store/file"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
	"github.com/Arjun0606/smolanalytics/internal/store/segment"
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
		serve(st, closeStore)
	case "demo":
		st := memory.New()
		if err := demo.Seed(st); err != nil {
			log.Fatal(err)
		}
		log.Printf("smolanalytics: seeded demo data (in-memory, not persisted)")
		serve(st, func() error { return nil })
	case "mcp":
		runMCP()
	default:
		fmt.Println("smolanalytics — product analytics in one binary")
		fmt.Println()
		fmt.Println("  smolanalytics demo    seed a realistic dataset + open a populated dashboard")
		fmt.Println("  smolanalytics serve   persist events from POST /v1/events to a durable log")
		fmt.Println("  smolanalytics mcp     MCP server over stdio — connect your Claude/Cursor and ask anything")
		fmt.Println()
		fmt.Println("  ADDR                      listen address (default :8080)")
		fmt.Println("  SMOLANALYTICS_DB          event log path (default ./smolanalytics.data)")
		fmt.Println("  SMOLANALYTICS_RETAIN_DAYS drop events older than N days (default: keep forever)")
		fmt.Println("  SMOLANALYTICS_MAX_EVENTS  keep only the newest N events resident (memory guardrail)")
		fmt.Println("  SMOLANALYTICS_COLD        dir for the scale tier: columnar segments, bounded RAM,")
		fmt.Println("                            history to billions of events (default: single-file log)")
		fmt.Println("  SMOLANALYTICS_SEAL_EVENTS events per columnar segment when COLD is set (default 50k)")
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
	if cold := os.Getenv("SMOLANALYTICS_COLD"); cold != "" {
		b, err := blob.NewLocal(cold)
		if err != nil {
			return nil, nil, err
		}
		s, err := segment.Open(dataPath(), b, envInt("SMOLANALYTICS_SEAL_EVENTS"))
		if err != nil {
			return nil, nil, err
		}
		log.Printf("smolanalytics: scale backend — hot log %s + columnar segments in %s (%d events)", dataPath(), cold, s.Count())
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

func serve(st store.Store, closeStore func() error) {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	app := api.New(st)
	app.SetWriteKey(os.Getenv("SMOLANALYTICS_WRITE_KEY"))
	if ins, err := insights.Open(dataPath() + ".insights.json"); err == nil {
		app.SetInsights(ins)
	} else {
		log.Printf("smolanalytics: saved reports disabled (%v)", err)
	}
	if coh, err := cohort.Open(dataPath() + ".cohorts.json"); err == nil {
		app.SetCohorts(coh)
	} else {
		log.Printf("smolanalytics: cohorts disabled (%v)", err)
	}
	if set, err := settings.Open(dataPath() + ".settings.json"); err == nil {
		// Default retention from env (the cloud sets this per plan) — only if the
		// operator hasn't already chosen one in the dashboard, which persists and wins.
		if d := envInt("SMOLANALYTICS_RETAIN_DAYS"); d > 0 && set.RetainDays() == 0 {
			if err := set.SetRetainDays(d); err == nil {
				log.Printf("smolanalytics: retention — keeping %d days of events", d)
			}
		}
		app.SetSettings(set)
		go pruneLoop(st, set)
	} else {
		log.Printf("smolanalytics: settings persistence disabled (%v)", err)
	}
	if al, err := audit.Open(dataPath() + ".audit.jsonl"); err == nil {
		app.SetAudit(al)
	} else {
		log.Printf("smolanalytics: audit log disabled (%v)", err)
	}
	if wh, err := webhook.Open(dataPath() + ".webhooks.json"); err == nil {
		app.SetWebhooks(wh)
		go dailyBrief(st, wh)
	} else {
		log.Printf("smolanalytics: webhooks disabled (%v)", err)
	}
	if al, err := alert.Open(dataPath() + ".alerts.json"); err == nil {
		app.SetAlerts(al)
		go alertLoop(app)
	} else {
		log.Printf("smolanalytics: alerts disabled (%v)", err)
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

	if os.Getenv("SMOLANALYTICS_PASSWORD") == "" {
		log.Printf("smolanalytics: WARNING — no SMOLANALYTICS_PASSWORD set; the dashboard, exports and MCP are UNAUTHENTICATED. Set a password (and a write key) before exposing this beyond localhost.")
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
		wh.DeliverAll(map[string]any{
			"type":     "daily_brief",
			"text":     insight.Text(findings),
			"findings": findings,
			"at":       time.Now().UTC(),
		})
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
	if err := mcp.New(st).ServeStdio(); err != nil {
		log.Fatal(err)
	}
}
