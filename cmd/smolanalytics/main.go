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
	"github.com/Arjun0606/smolanalytics/internal/demo"
	"github.com/Arjun0606/smolanalytics/internal/mcp"
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

	log.Printf("smolanalytics: dashboard on http://localhost%s · MCP at /mcp", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
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
