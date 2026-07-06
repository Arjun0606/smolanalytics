# smolanalytics from your backend

No SDK needed: ingestion is one endpoint, `POST /v1/events`. Send server-side events (the
ones the browser never sees: payments, webhooks, cron jobs, API usage) from any language.

Use the **same `distinct_id`** you use client-side so a user's browser and server events
join into one funnel. Send one event or a batch (an array, up to 10k).

```
POST https://YOUR_HOST/v1/events
Authorization: Bearer YOUR_WRITE_KEY
Content-Type: application/json

{ "name": "checkout", "distinct_id": "user_123", "properties": { "amount": 29, "plan": "pro" } }
```

## Node

```js
await fetch(`${process.env.SMOL_HOST}/v1/events`, {
  method: "POST",
  headers: { "Content-Type": "application/json", Authorization: `Bearer ${process.env.SMOL_KEY}` },
  body: JSON.stringify({ name: "checkout", distinct_id: userId, properties: { amount: 29 } }),
});
```

## Python

```python
import requests
requests.post(f"{HOST}/v1/events",
    headers={"Authorization": f"Bearer {KEY}"},
    json={"name": "signup", "distinct_id": user_id, "properties": {"plan": "pro"}})
```

## Go

```go
body, _ := json.Marshal(map[string]any{"name": "checkout", "distinct_id": userID, "properties": map[string]any{"amount": 29}})
req, _ := http.NewRequest("POST", host+"/v1/events", bytes.NewReader(body))
req.Header.Set("Authorization", "Bearer "+key)
req.Header.Set("Content-Type", "application/json")
http.DefaultClient.Do(req)
```

## Ruby

```ruby
require "net/http"; require "json"
uri = URI("#{HOST}/v1/events")
Net::HTTP.post(uri, { name: "signup", distinct_id: user_id }.to_json,
  "Authorization" => "Bearer #{KEY}", "Content-Type" => "application/json")
```

## PHP

```php
$ch = curl_init("$host/v1/events");
curl_setopt_array($ch, [
  CURLOPT_POST => true,
  CURLOPT_HTTPHEADER => ["Authorization: Bearer $key", "Content-Type: application/json"],
  CURLOPT_POSTFIELDS => json_encode(["name" => "checkout", "distinct_id" => $userId]),
]);
curl_exec($ch);
```

## A batch (any language)

```json
[
  { "name": "signup",   "distinct_id": "u_2", "properties": { "plan": "free" } },
  { "name": "activate", "distinct_id": "u_2" },
  { "name": "checkout", "distinct_id": "u_2", "properties": { "amount": 29 } }
]
```

## Then ask it in your editor

`smolanalytics connect`, then: *"what's the conversion from signup to checkout, and how long
does it take?"* Your own model, your real data. See the [main README](../README.md).
