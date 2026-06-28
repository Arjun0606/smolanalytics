// Command smolanalytics is the single binary: product analytics you can run with
// one command, no cluster. `demo` seeds a populated dashboard in 60 seconds;
// `serve` persists events to a durable log; `mcp` lets your own AI read it.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/api"
	"github.com/Arjun0606/smolanalytics/internal/audit"
	"github.com/Arjun0606/smolanalytics/internal/cohort"
	"github.com/Arjun0606/smolanalytics/internal/demo"
	"github.com/Arjun0606/smolanalytics/internal/insights"
	"github.com/Arjun0606/smolanalytics/internal/mcp"
	"github.com/Arjun0606/smolanalytics/internal/settings"
	"github.com/Arjun0606/smolanalytics/internal/store"
	"github.com/Arjun0606/smolanalytics/internal/store/file"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

func main() {
	cmd := ""
	if len(os.Args) >= 2 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		fs, err := file.Open(dataPath())
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("smolanalytics: %d events loaded from %s", fs.Count(), dataPath())
		serve(fs, fs.Close)
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
		fmt.Println("  ADDR             listen address (default :8080)")
		fmt.Println("  SMOLANALYTICS_DB event log path (default ./smolanalytics.data)")
		fmt.Println("  the running server also speaks MCP at POST /mcp (Streamable HTTP)")
	}
}

func dataPath() string {
	if p := os.Getenv("SMOLANALYTICS_DB"); p != "" {
		return p
	}
	return "smolanalytics.data"
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
	log.Printf("smolanalytics: dashboard on http://localhost%s · MCP at /mcp", addr)
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
