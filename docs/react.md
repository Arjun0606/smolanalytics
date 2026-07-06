# smolanalytics for React

Self-hosted product analytics for your React app (Vite, CRA, or any SPA) that you ask in
plain English from your editor. Autocaptures pageviews (SPA route changes included) and
clicks; add `track()` for funnels.

## 1. Load the SDK

Simplest: add to `index.html`:

```html
<!-- index.html -->
<script src="https://YOUR_HOST/sdk.js"></script>
<script>smolanalytics.init("YOUR_WRITE_KEY", { host: "https://YOUR_HOST" });</script>
```

Or load it once from your entry file so it's bundled with your app:

```tsx
// src/analytics.ts
export function initAnalytics() {
  if (document.getElementById("smol")) return;
  const s = document.createElement("script");
  s.id = "smol"; s.src = "https://YOUR_HOST/sdk.js";
  s.onload = () => (window as any).smolanalytics.init("YOUR_WRITE_KEY", { host: "https://YOUR_HOST" });
  document.head.appendChild(s);
}
// src/main.tsx → call initAnalytics() once, before render
```

Pageviews and clicks are now captured, and SPA navigations (React Router, TanStack Router,
etc.) are picked up automatically, no per-route wiring.

## 2. Track the moments that matter

```tsx
declare global { interface Window { smolanalytics: any } }

function onSignup(userId: string) {
  window.smolanalytics.identify(userId);            // ties their events together
  window.smolanalytics.track("signup", { plan: "pro" });
}
// at your core aha moment:
window.smolanalytics.track("activate");
```

A tiny hook, if you like:

```tsx
export const useTrack = () => (name: string, props?: object) => window.smolanalytics?.track(name, props);
```

## 3. Ask it in your editor

```sh
smolanalytics connect      # wires it into Cursor / Claude Code / VS Code / …
```
Then ask, right where you code: *"how's activation this week, and is the paid plan
converting better than free?"* Your own model answers from your real data, no dashboards to
build. Full report list in the [main README](../README.md).
