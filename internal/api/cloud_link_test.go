package api

import (
	"strings"
	"testing"
)

// THE ONE LINK OUT OF AN INSTANCE MUST LEAD BACK INTO THE ACCOUNT.
//
// This used to guard the dashboard header's "Cloud ↗" link. The dashboard is gone — this instance
// is a data layer now — but the property survived the deletion and matters more than it did: the
// root page exists ONLY to tell somebody where their product went, so a wrong link here is the
// whole page failing.
//
// Two cases, and they are different products. A self-hoster has no project and must be sent to the
// marketing site rather than a tenant URL that means nothing to them. A hosted customer is
// provisioned with SMOLANALYTICS_CLOUD_URL pointing at their own project page, which is where the
// test runs, the suite and the tracking plan actually live.

func TestRootPageSendsASelfHosterToTheMarketingSite(t *testing.T) {
	body := rootPage("https://smolanalytics.com", "https://my-instance.fly.dev")
	if !strings.Contains(body, `href="https://smolanalytics.com"`) {
		t.Error("a self-hosted instance must link to https://smolanalytics.com, not to a tenant URL")
	}
}

func TestRootPageSendsAHostedCustomerToTheirOwnProject(t *testing.T) {
	// Without this the instance is a dead end: the project page used to redirect here, so a reader
	// who lands on it has no way back to their own account.
	const project = "https://smolanalytics.com/projects/prj_abc123"
	body := rootPage(project, "https://my-instance.fly.dev")
	if !strings.Contains(body, `href="`+project+`"`) {
		t.Errorf("the root page must link to the configured project page, got:\n%s", body)
	}
}

func TestRootPageSaysWhatTheInstanceIsAndIsNot(t *testing.T) {
	// Somebody landing here has almost always bookmarked the old dashboard. Meeting a bare 404, or
	// a page that looks like a broken dashboard, reads as the service being down.
	body := rootPage("https://smolanalytics.com", "https://my-instance.fly.dev")
	if !strings.Contains(body, "not a dashboard") {
		t.Error("the page does not say what this instance is")
	}
	// It names the surfaces that DO exist here, with this instance's own address rather than a
	// placeholder somebody would have to translate.
	for _, want := range []string{"https://my-instance.fly.dev/mcp", "https://my-instance.fly.dev/v1/events"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not name %s", want)
		}
	}
}

func TestRootPageIsNotIndexable(t *testing.T) {
	// Every tenant serves a near-identical copy of this page. Letting them be indexed spreads
	// hundreds of duplicates across the web under fly.dev subdomains.
	if !strings.Contains(rootPage("https://smolanalytics.com", "https://x.fly.dev"), `name="robots" content="noindex"`) {
		t.Error("the instance root page is indexable")
	}
}
