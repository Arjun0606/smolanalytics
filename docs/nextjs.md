# smolanalytics for Next.js

Privacy-first product analytics for your Next.js app, self-hosted, that you ask in plain
English from your editor. Autocaptures pageviews (including client-side route changes) and
clicks; add `track()` for funnels.

## 1. Load the SDK (App Router)

Add it to your root layout with `next/script`:

```tsx
// app/layout.tsx
import Script from "next/script";

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
      <Script src="https://YOUR_HOST/sdk.js" strategy="afterInteractive" />
      <Script id="smol-init" strategy="afterInteractive">
        {`smolanalytics.init("YOUR_WRITE_KEY", { host: "https://YOUR_HOST" });`}
      </Script>
    </html>
  );
}
```

That's it: pageviews and clicks are now captured on every route, App Router or Pages
Router (SPA navigations included).

<details><summary>Pages Router</summary>

```tsx
// pages/_app.tsx
import Script from "next/script";
export default function App({ Component, pageProps }) {
  return (<>
    <Component {...pageProps} />
    <Script src="https://YOUR_HOST/sdk.js" strategy="afterInteractive" />
    <Script id="smol-init">{`smolanalytics.init("YOUR_WRITE_KEY", { host: "https://YOUR_HOST" });`}</Script>
  </>);
}
```
</details>

## 2. Track the moments that matter

TypeScript note: the SDK attaches itself to `window`, so give yourself a tiny typed
helper once and call it from any client component:

```ts
// lib/analytics.ts
export function track(name: string, props?: Record<string, unknown>) {
  if (typeof window !== "undefined") (window as any).smolanalytics?.track(name, props);
}
export function identify(userId: string) {
  if (typeof window !== "undefined") (window as any).smolanalytics?.identify(userId);
}
```
 (client)

Anywhere in a client component (`"use client"`), the SDK is on `window`:

```tsx
"use client";
declare global { interface Window { smolanalytics: any } }

// on signup
window.smolanalytics.track("signup", { plan: "pro" });
// on login: ties this user's events together
window.smolanalytics.identify("user_123", { email: "a@b.com" });
// at your core aha moment
window.smolanalytics.track("activate");
```

## 3. Server-side events (Route Handlers, Server Actions, webhooks)

Things that happen on the server (a Stripe webhook, a payment) post directly:

```ts
// app/api/checkout/route.ts (or a Server Action)
await fetch(`${process.env.SMOLANALYTICS_HOST}/v1/events`, {
  method: "POST",
  headers: { "Content-Type": "application/json", Authorization: `Bearer ${process.env.SMOLANALYTICS_KEY}` },
  body: JSON.stringify({ name: "checkout", distinct_id: userId, properties: { amount: 29 } }),
});
```

Use the **same `distinct_id`** you pass to `identify()` so client and server events join up.

## 4. Ask it in your editor

```sh
smolanalytics connect      # wires it into Cursor / Claude Code / VS Code / …
```
Then, right where you write code: *"what's my signup → checkout conversion, and where do
Next.js users drop off?"* Your own model answers from your real data. See the
[main README](../README.md) for the full report list.
