// Package botua identifies crawler/bot user agents so autocaptured web traffic
// ($pageview/$click) doesn't inflate every report. List-based forever — no ML, no
// behavioral scoring (see docs/design: predictable beats clever). Backend events are
// NEVER filtered by UA: server SDKs legitimately send Go-http-client/curl/etc.
package botua

import "strings"

// substrings matched case-insensitively against the User-Agent. Curated for the
// crawlers that actually hit small sites, including the 2026 AI-crawler wave.
var patterns = []string{
	// search engines
	"googlebot", "bingbot", "yandexbot", "baiduspider", "duckduckbot", "applebot",
	// AI crawlers (kept distinct from AI *referrals* — a human clicking out of
	// ChatGPT is real traffic; GPTBot fetching your page is not)
	"gptbot", "claudebot", "claude-web", "perplexitybot", "google-extended",
	"ccbot", "bytespider", "amazonbot", "meta-externalagent", "oai-searchbot",
	// SEO/monitoring crawlers
	"ahrefsbot", "semrushbot", "mj12bot", "dotbot", "petalbot", "screaming frog",
	"uptimerobot", "pingdom", "statuscake", "site24x7", "betteruptime",
	// link unfurlers
	"facebookexternalhit", "twitterbot", "linkedinbot", "slackbot", "discordbot",
	"telegrambot", "whatsapp", "skypeuripreview",
	// headless/automation signatures browsers never send
	"headlesschrome", "lighthouse", "phantomjs", "selenium", "playwright", "puppeteer",
	// generic
	"crawler", "spider", "scraper", "bot/", "bot;",
}

// IsBot reports whether ua looks like an automated agent. Empty UA on web traffic
// is also treated as a bot — every real browser sends one.
func IsBot(ua string) bool {
	if ua == "" {
		return true
	}
	l := strings.ToLower(ua)
	for _, p := range patterns {
		if strings.Contains(l, p) {
			return true
		}
	}
	return false
}
