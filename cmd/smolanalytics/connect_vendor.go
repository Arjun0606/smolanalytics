package main

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/Arjun0606/smolanalytics/internal/connector"
)

// KEEP THE ANALYTICS YOU ALREADY HAVE.
//
// internal/connector was built complete — the aggregate contract, the PostHog reader, the
// watermarked syncer, even its own ticker loop — and NOTHING CALLED IT. Not a cron, not the
// engine, not a command. A feature nobody can reach is one the operator reasonably assumes was
// never built, and this one is the centre of the product's own pitch: you keep PostHog, we read
// it. This file is the wire.
//
// It reads AGGREGATES, not raw events: daily counts per event, dimension marginals for the events
// that matter, and money counters. That is a couple of bounded queries per day instead of paging
// through a vendor's raw export, which is both cheaper for them and permitted — PostHog's own docs
// call the raw events endpoint deprecated and cap offset pagination.
//
// Env-gated and off by default, like every other optional store. Nothing happens unless an
// operator sets SMOLANALYTICS_CONNECT_VENDOR, and when it is set the log SAYS what is being read
// and from where, because a background job that silently talks to a third party on your behalf is
// not something to discover later.
const (
	connVendorEnv  = "SMOLANALYTICS_CONNECT_VENDOR"  // posthog
	connHostEnv    = "SMOLANALYTICS_CONNECT_HOST"    // https://us.posthog.com (or eu)
	connProjectEnv = "SMOLANALYTICS_CONNECT_PROJECT" // the vendor's project id
	connKeyEnv     = "SMOLANALYTICS_CONNECT_KEY"     // a READ-scoped personal API key
)

// startVendorSync begins the background read, or explains precisely why it did not.
// Returns the syncer so a caller can trigger a read on demand; nil when not configured.
func startVendorSync(ctx context.Context, statePath string) *connector.Syncer {
	vendor := strings.ToLower(strings.TrimSpace(os.Getenv(connVendorEnv)))
	if vendor == "" {
		return nil // not configured: the common case, and silent on purpose
	}

	host := strings.TrimSpace(os.Getenv(connHostEnv))
	project := strings.TrimSpace(os.Getenv(connProjectEnv))
	apiKey := strings.TrimSpace(os.Getenv(connKeyEnv))

	// Say exactly which piece is missing. "connector disabled" with no reason is the kind of
	// message that costs somebody an afternoon.
	var missing []string
	if project == "" {
		missing = append(missing, connProjectEnv)
	}
	if apiKey == "" {
		missing = append(missing, connKeyEnv)
	}
	if len(missing) > 0 {
		log.Printf("smolanalytics: %s=%s is set but %s %s missing, so nothing is being read",
			connVendorEnv, vendor, strings.Join(missing, " and "), plural2(len(missing)))
		return nil
	}

	var f connector.Fetcher
	switch vendor {
	case "posthog":
		f = &connector.PostHog{Host: host, Project: project, Key: connector.NewCredential(apiKey)}
	default:
		// Named vendors only. Guessing at an unknown one would produce a confident stream of
		// nothing, which reads as "the product is broken" rather than "that is not supported".
		log.Printf("smolanalytics: %s=%q is not a vendor this build reads (posthog)", connVendorEnv, vendor)
		return nil
	}

	store, err := connector.OpenFileStore(statePath)
	if err != nil {
		log.Printf("smolanalytics: reading your %s is off (%v)", vendor, err)
		return nil
	}

	s := &connector.Syncer{
		Fetcher: f,
		Store:   store,
		Opts:    connector.SyncOpts{Project: project},
	}
	log.Printf("smolanalytics: reading %s project %s in aggregate; your events stay where they are",
		vendor, project)

	go s.Run(ctx, func(err error) {
		// Errors are logged and the loop continues: a vendor rate limit or a rotated key must not
		// take the instance down, and the syncer already backs off.
		log.Printf("smolanalytics: %s read failed, will retry (%v)", vendor, err)
	})
	return s
}

func plural2(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}
