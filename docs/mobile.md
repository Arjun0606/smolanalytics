# smolanalytics for mobile

There are published native SDKs for every mobile platform, each with an offline-safe
persisted queue, batching, automatic retries, sessions, lifecycle events (`app_open` +
flush on background), device context on every event, a cookieless anonymous id by default,
and a first-class `screen()` method. Install the one for your platform and you're done.

No SDK for your stack? Ingestion is one endpoint (`POST /v1/events`), so anything that can
make an HTTP request works — see [the raw fallback](#no-sdk-raw-post) at the bottom.

Use a stable id per user via `identify()` (your user id) so their events tie together across
devices and sessions; before that, events are attributed to a rotating anonymous id.

## iOS (Swift) — Swift Package Manager

Add the package in Xcode (File → Add Package Dependencies…) or in `Package.swift`:

```swift
.package(url: "https://github.com/Arjun0606/smolanalytics-swift", from: "0.1.0")
```

```swift
import SmolAnalytics

// once, at launch (e.g. in your App init / AppDelegate)
SmolAnalytics.initialize(writeKey: "sa_your_write_key", host: "https://YOUR_HOST")

SmolAnalytics.track("signup", ["plan": "pro"])
SmolAnalytics.screen("Checkout")
SmolAnalytics.identify("user-123", ["email": "a@b.com"])   // on login
SmolAnalytics.reset()                                       // on logout
```

`app_open` and foreground/background are tracked automatically; pass `lifecycle: false` to
`initialize` to opt out. The event queue persists in `UserDefaults`, so nothing is lost if
the app is killed offline.

## Android (Kotlin) — JitPack

In `settings.gradle.kts`, add the JitPack repo:

```kotlin
repositories { google(); mavenCentral(); maven("https://jitpack.io") }
```

In your app's `build.gradle.kts`:

```kotlin
implementation("com.github.Arjun0606:smolanalytics-android:v0.1.0")
```

```kotlin
import com.smolanalytics.SmolAnalytics

// once, in Application.onCreate()
SmolAnalytics.initialize(this, "sa_your_write_key", "https://YOUR_HOST")

SmolAnalytics.track("signup", mapOf("plan" to "pro"))
SmolAnalytics.screen("Checkout")
SmolAnalytics.identify("user-123", mapOf("email" to "a@b.com"))  // on login
SmolAnalytics.reset()                                             // on logout
```

Add the `INTERNET` permission to your manifest. No OkHttp or androidx dependency — the SDK
uses `HttpURLConnection` and persists its queue with `SharedPreferences`.

## React Native / Expo — npm

```sh
npm install smolanalytics-react-native
npm install @react-native-async-storage/async-storage   # for offline persistence
```

```ts
import smol from "smolanalytics-react-native";

// once, at app start
smol.init("sa_your_write_key", { host: "https://YOUR_HOST" });

smol.track("signup", { plan: "pro" });
smol.screen("Checkout");
smol.identify("user-123", { email: "a@b.com" });   // on login
smol.reset();                                        // on logout
```

## Flutter — pub.dev

In `pubspec.yaml`:

```yaml
dependencies:
  smolanalytics: ^0.1.0
```

```dart
import 'package:smolanalytics/smolanalytics.dart';

// once, in main()
Smolanalytics.init("sa_your_write_key", host: "https://YOUR_HOST");

Smolanalytics.track("signup", {"plan": "pro"});
Smolanalytics.screen("Checkout");
Smolanalytics.identify("user-123", {"email": "a@b.com"});   // on login
Smolanalytics.reset();                                       // on logout
```

For persistence across restarts, pass a `store` backed by `shared_preferences` (see the
[package README](https://pub.dev/packages/smolanalytics)); the SDK batches and retries either way.

## No SDK? Raw POST

Ingestion is one endpoint, so any HTTP client sends events directly. This is the fallback
when there's no native SDK for your stack — the SDKs above add the queueing, batching, and
lifecycle handling you'd otherwise write yourself.

```swift
// iOS, no SDK
var req = URLRequest(url: URL(string: "\(host)/v1/events")!)
req.httpMethod = "POST"
req.setValue("Bearer \(key)", forHTTPHeaderField: "Authorization")
req.setValue("application/json", forHTTPHeaderField: "Content-Type")
req.httpBody = try? JSONSerialization.data(withJSONObject: ["name": "signup", "distinct_id": userId])
URLSession.shared.dataTask(with: req).resume()
```

You can POST a single event or an array (up to 10,000) in one request. `distinct_id` is the
only required field besides `name`.

## Then ask it in your editor

`smolanalytics connect`, then: *"what's my activation rate on iOS vs Android?"* answered by
your own model from your real data. See the [main README](../README.md).
