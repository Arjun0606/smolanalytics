# smolanalytics for mobile

No heavy SDK. Ingestion is one endpoint — `POST /v1/events` — so a native app sends events
with the HTTP client it already has. Use a stable `distinct_id` per user (your user id, or a
generated id stored in Keychain / SharedPreferences) so their events tie together.

## iOS (Swift)

```swift
func track(_ name: String, _ props: [String: Any] = [:], distinctId: String) {
    var req = URLRequest(url: URL(string: "\(host)/v1/events")!)
    req.httpMethod = "POST"
    req.setValue("Bearer \(key)", forHTTPHeaderField: "Authorization")
    req.setValue("application/json", forHTTPHeaderField: "Content-Type")
    req.httpBody = try? JSONSerialization.data(withJSONObject: [
        "name": name, "distinct_id": distinctId, "properties": props,
    ])
    URLSession.shared.dataTask(with: req).resume()
}

// track("signup", ["plan": "pro"], distinctId: userId)
// track("activate", distinctId: userId)
```

## Android (Kotlin / OkHttp)

```kotlin
fun track(name: String, props: Map<String, Any> = emptyMap(), distinctId: String) {
    val payload = JSONObject(mapOf("name" to name, "distinct_id" to distinctId, "properties" to JSONObject(props)))
    val body = payload.toString().toRequestBody("application/json".toMediaType())
    val req = Request.Builder().url("$host/v1/events")
        .addHeader("Authorization", "Bearer $key").post(body).build()
    client.newCall(req).enqueue(object : Callback {
        override fun onFailure(call: Call, e: IOException) {}
        override fun onResponse(call: Call, r: Response) { r.close() }
    })
}
```

## React Native / Expo

```ts
export const track = (name: string, props = {}, distinctId: string) =>
  fetch(`${HOST}/v1/events`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${KEY}` },
    body: JSON.stringify({ name, distinct_id: distinctId, properties: props }),
  });
```

## Tips

- **Batch to save battery/network.** POST an array (up to 10k events) instead of one request
  per event — queue them and flush on background/foreground.
- **Screens are events too.** `track("screen", { name: "Checkout" }, ...)` gives you
  screen-flow paths, the mobile version of pageviews.

## Then ask it in your editor

`smolanalytics connect`, then: *"what's my activation rate on iOS vs Android?"* — answered by
your own model from your real data. See the [main README](../README.md).
