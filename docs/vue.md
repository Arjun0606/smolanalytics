# smolanalytics for Vue

Self-hosted product analytics for your Vue / Nuxt app that you ask in plain English from
your editor. Autocaptures pageviews (SPA route changes included) and clicks.

## 1. Load the SDK

Add to `index.html`:

```html
<!-- index.html -->
<script src="https://YOUR_HOST/sdk.js"></script>
<script>smolanalytics.init("YOUR_WRITE_KEY", { host: "https://YOUR_HOST" });</script>
```

**Nuxt**: add it in `nuxt.config.ts`:

```ts
export default defineNuxtConfig({
  app: { head: { script: [
    { src: "https://YOUR_HOST/sdk.js" },
    { children: `smolanalytics.init("YOUR_WRITE_KEY", { host: "https://YOUR_HOST" });` },
  ] } },
});
```

Pageviews and clicks are captured, Vue Router navigations included.

## 2. Track the moments that matter

```ts
// anywhere: the SDK is on window
window.smolanalytics.identify(userId);            // on login
window.smolanalytics.track("signup", { plan: "pro" });
window.smolanalytics.track("activate");           // core aha moment
```

## 3. Ask it in your editor

```sh
smolanalytics connect
```
Then: *"where are users dropping off, and which traffic source converts best?"* answered
by your own model, from your real data. See the [main README](../README.md).
