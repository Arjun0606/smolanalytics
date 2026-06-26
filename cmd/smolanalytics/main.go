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
	default:
		fmt.Println("smolanalytics — product analytics in one binary")
		fmt.Println()
		fmt.Println("  smolanalytics demo    seed a realistic dataset + open a populated dashboard")
		fmt.Println("  smolanalytics serve   run empty, send events to POST /v1/events")
		fmt.Println()
		fmt.Println("  ADDR   listen address (default :8080)")
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
