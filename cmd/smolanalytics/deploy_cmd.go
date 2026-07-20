package main

// `smolanalytics deploy` — record a deploy marker so you can ask "did that ship move the
// metric?". Defaults to the current git HEAD (sha + subject + author), so one line in CI is
// all it takes:  smolanalytics deploy   (with SMOLANALYTICS_HOST + SMOLANALYTICS_WRITE_KEY set)
// This is the open-source, no-cloud path to the wedge: every self-hoster gets deploy-aware
// analytics from a git hook or a CI step, then asks their editor over MCP.

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

func deployCmd(args []string) {
	fs := flag.NewFlagSet("deploy", flag.ExitOnError)
	host := fs.String("host", envOrDefault("SMOLANALYTICS_HOST", "http://127.0.0.1:8080"), "instance URL")
	key := fs.String("key", os.Getenv("SMOLANALYTICS_WRITE_KEY"), "write key (public ingest key)")
	msg := fs.String("message", "", "what shipped (default: the git commit subject)")
	sha := fs.String("sha", "", "git sha (default: the current HEAD)")
	_ = fs.Parse(args)

	if *sha == "" {
		*sha = gitOut("rev-parse", "HEAD")
	}
	if *msg == "" {
		*msg = gitOut("log", "-1", "--format=%s")
	}
	author := gitOut("log", "-1", "--format=%an")
	if *sha == "" && *msg == "" {
		log.Fatal("deploy: no --message and not in a git repo — pass --message \"what shipped\"")
	}

	body, _ := json.Marshal(map[string]string{"sha": *sha, "message": *msg, "author": author, "source": "cli"})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(*host, "/")+"/v1/deploys", bytes.NewReader(body))
	if err != nil {
		log.Fatalf("deploy: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if *key != "" {
		req.Header.Set("Authorization", "Bearer "+*key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatalf("deploy: couldn't reach %s (%v) — is the instance up, and SMOLANALYTICS_HOST set?", *host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		log.Fatalf("deploy: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	fmt.Printf("recorded deploy %s %q\n", shortSHA(*sha), *msg)
	fmt.Println("ask your editor \"did my last deploy move signups?\", or GET /v1/deploys?event=<metric>")
}

func gitOut(args ...string) string {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}
