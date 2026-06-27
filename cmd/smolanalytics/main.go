// Command smolanalytics is the single binary: product analytics you can run with
// one command, no cluster. `demo` seeds a populated dashboard in 60 seconds.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/api"
	"github.com/Arjun0606/smolanalytics/internal/demo"
	"github.com/Arjun0606/smolanalytics/internal/mcp"
	"github.com/Arjun0606/smolanalytics/internal/store/memory"
)

func main() {
	cmd := ""
	if len(os.Args) >= 2 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		run(false)
	case "demo":
		run(true)
	case "mcp":
		runMCP()
	default:
		fmt.Println("smolanalytics — product analytics in one binary")
		fmt.Println()
		fmt.Println("  smolanalytics demo    seed a realistic dataset + open a populated dashboard")
		fmt.Println("  smolanalytics serve   run empty, send events to POST /v1/events")
		fmt.Println("  smolanalytics mcp     MCP server over stdio — connect your Claude/Cursor and ask anything")
		fmt.Println()
		fmt.Println("  ADDR   listen address (default :8080)")
		fmt.Println("  the running server also speaks MCP at POST /mcp (Streamable HTTP)")
	}
}

// runMCP serves the analytics over MCP on stdio for a local Claude Desktop / Cursor.
// Seeded with demo data so it works the moment you connect it; production data
// flows in via the HTTP server's shared store (and persistence, coming next).
func runMCP() {
	st := memory.New()
	if err := demo.Seed(st); err != nil {
		log.Fatal(err)
	}
	if err := mcp.New(st).ServeStdio(); err != nil {
		log.Fatal(err)
	}
}

func run(seed bool) {
	st := memory.New()
	if seed {
		if err := demo.Seed(st); err != nil {
			log.Fatal(err)
		}
		log.Printf("smolanalytics: seeded demo data")
	}
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           api.New(st).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Printf("smolanalytics: dashboard on http://localhost%s", addr)
	log.Fatal(srv.ListenAndServe())
}
