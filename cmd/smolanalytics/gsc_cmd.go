package main

// `smolanalytics gsc` — connect Google Search Console in one command.
//
//	smolanalytics gsc auth     browser consent → pick property → done
//	smolanalytics gsc status   what's connected, when data was last pulled
//
// BYO OAuth client (standard for self-hosted tools): create an OAuth client of
// type "Web application" in Google Cloud Console with redirect URI
// http://127.0.0.1:8931/callback, enable the Search Console API, then export
// SMOLANALYTICS_GSC_CLIENT_ID and SMOLANALYTICS_GSC_CLIENT_SECRET.

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"github.com/Arjun0606/smolanalytics/internal/gsc"
)

const gscRedirect = "http://127.0.0.1:8931/callback"

func gscCmd(args []string) {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	store, err := gsc.Open(dataPath() + ".gsc.json")
	if err != nil {
		log.Fatal(err)
	}
	switch sub {
	case "auth":
		gscAuth(store)
	case "status":
		rows, _, site, fetched := store.Snapshot()
		if !store.Connected() {
			fmt.Println("not connected — run `smolanalytics gsc auth`")
			fmt.Println("(needs SMOLANALYTICS_GSC_CLIENT_ID + _SECRET — see `smolanalytics gsc`)")
			return
		}
		fmt.Printf("connected to %s\n", site)
		if fetched.IsZero() {
			fmt.Println("no data pulled yet — the server polls every 12h, or restart it to pull now")
		} else {
			fmt.Printf("%d queries cached, fetched %s\n", len(rows), fetched.Format(time.RFC822))
		}
	default:
		fmt.Println("smolanalytics gsc — Google Search Console integration")
		fmt.Println()
		fmt.Println("  1. console.cloud.google.com → create OAuth client (Web application)")
		fmt.Println("     with redirect URI " + gscRedirect + ", enable the Search Console API")
		fmt.Println("  2. export SMOLANALYTICS_GSC_CLIENT_ID=... SMOLANALYTICS_GSC_CLIENT_SECRET=...")
		fmt.Println("  3. smolanalytics gsc auth")
		fmt.Println()
		fmt.Println("Then the running server pulls top search queries every 12h; ask your AI")
		fmt.Println("`search_console_report`, or see the Search card on the dashboard.")
	}
}

func gscAuth(store *gsc.Store) {
	creds, ok := gsc.CredsFromEnv()
	if !ok {
		log.Fatal("gsc auth: set SMOLANALYTICS_GSC_CLIENT_ID and SMOLANALYTICS_GSC_CLIENT_SECRET first (run `smolanalytics gsc` for the 3-step setup)")
	}

	codeCh := make(chan string, 1)
	srv := &http.Server{Addr: "127.0.0.1:8931"}
	http.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "no code in callback — try again", http.StatusBadRequest)
			return
		}
		fmt.Fprintln(w, "connected — you can close this tab and return to the terminal")
		codeCh <- code
	})
	go func() { _ = srv.ListenAndServe() }()
	defer func() { _ = srv.Close() }()

	authURL := gsc.AuthURL(creds, gscRedirect)
	fmt.Println("opening browser for Google consent…")
	openBrowser(authURL)
	fmt.Println("(if it didn't open, visit:)\n" + authURL)

	var code string
	select {
	case code = <-codeCh:
	case <-time.After(5 * time.Minute):
		log.Fatal("gsc auth: timed out waiting for the browser consent")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	refresh, err := gsc.Exchange(ctx, creds, code, gscRedirect)
	if err != nil {
		log.Fatal(err)
	}

	sites, err := gsc.ListSites(ctx, creds, refresh)
	if err != nil {
		log.Fatal(err)
	}
	if len(sites) == 0 {
		log.Fatal("this Google account has no Search Console properties — add your site at search.google.com/search-console first")
	}
	site := sites[0]
	if len(sites) > 1 {
		fmt.Println("your Search Console properties:")
		for i, s := range sites {
			fmt.Printf("  %d) %s\n", i+1, s)
		}
		fmt.Print("pick one [1]: ")
		var in string
		_, _ = fmt.Fscanln(os.Stdin, &in)
		if n, err := strconv.Atoi(in); err == nil && n >= 1 && n <= len(sites) {
			site = sites[n-1]
		}
	}
	if err := store.SetGrant(refresh, site); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("connected to %s ✓\n", site)

	fmt.Println("pulling the first 28 days of queries…")
	rows, err := gsc.FetchQueries(ctx, creds, refresh, site, 28)
	if err != nil {
		fmt.Printf("first pull failed (%v) — the server will retry on its 12h schedule\n", err)
		return
	}
	if err := store.SetRows(rows); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%d queries cached ✓ — ask your AI `search_console_report`, or open the dashboard\n", len(rows))
}

func openBrowser(u string) {
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("open", u).Start()
	case "linux":
		_ = exec.Command("xdg-open", u).Start()
	case "windows":
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	}
}
