# Revenue: turning "480 people" into "$14,400/mo"

The investigation ranks findings by how much they cost you. Without revenue it can only count
people:

```
signup fell 28% on 2026-06-24     ~300 people/mo
```

With revenue it prices them, and the ranking changes — the expensive regression leads even when
a noisier one affected more people:

```
checkout fell 33% on 2026-06-27   ~$5,850/mo · 195 people
  people affected × mean revenue per person, from the amount property on this event
```

There are two ways to get revenue in. Either works; the second needs no code at all.

---

## 1. Put an amount on the event you already track

If your app already calls `track("checkout")`, add the money:

```js
smolanalytics.track("checkout", { amount: 29, plan: "pro" });
```

`amount` is the property the instrumenter teaches and the docs use. `revenue`, `value`, `price`
and `total` are also recognised, in that order of preference.

It must be a **number**, not a string. `"29"` is ignored on purpose: the filter engine would not
match it as a number either, so summing it here would make the revenue report and a filtered
report disagree about the same events. If your amounts are strings, revenue sizing declines to
fire rather than producing a figure nothing else can reproduce.

---

## 2. Point your payment provider's webhook at us

No code. Your processor already knows exactly what everyone paid, and this is usually more
accurate than anything the browser can tell you — it includes renewals, upgrades, and payments
that happened while nobody had your site open.

**Set the signing secret**, then restart:

| Provider | Environment variable |
|---|---|
| Stripe | `SMOLANALYTICS_STRIPE_SECRET` |
| Lemon Squeezy | `SMOLANALYTICS_LEMONSQUEEZY_SECRET` |
| Polar | `SMOLANALYTICS_POLAR_SECRET` |
| Dodo Payments | `SMOLANALYTICS_DODO_SECRET` |

**Add the endpoint** in your provider's own dashboard — nobody has to approve anything:

```
https://your-instance/v1/revenue/stripe
https://your-instance/v1/revenue/lemonsqueezy
https://your-instance/v1/revenue/polar
https://your-instance/v1/revenue/dodo
```

Subscribe to the completed-payment events. Everything else that arrives is accepted, ignored, and
answered `200` — a non-2xx would make the provider retry forever and eventually disable your
endpoint, so "we deliberately didn't store this" must never look like a failure.

### The part that actually matters: the join

A payment payload carries a **billing** identity — a Stripe customer id, an email — and never your
analytics `distinct_id`. If the two never meet, revenue lands against people who appear in no
funnel, and every revenue-per-person figure is wrong.

So pass your `distinct_id` through the checkout:

```js
// Stripe: the purpose-built field
stripe.checkout.sessions.create({ client_reference_id: distinctId, /* … */ });

// Lemon Squeezy
{ checkout_data: { custom: { distinct_id: distinctId } } }

// Polar / Dodo
{ metadata: { distinct_id: distinctId } }
```

Failing that, it falls back to the provider's customer id, then the email — and records **which**
in an `identity_source` property, because a figure joined by email is worth less than one joined
by an id you supplied, and you should be able to see which you are looking at. An email match can
be merged later: if your app calls `identify()` with that address, the alias table joins them.

A payment with no identity at all is **not stored**, and the response says so. Inventing a
synthetic person would add someone who appears in no funnel and quietly skew the mean.

### What lands

One ordinary event, on the same append-only log as everything else:

```json
{
  "name": "payment",
  "distinct_id": "u_123",
  "properties": {
    "amount": 29,
    "currency": "USD",
    "provider": "stripe",
    "kind": "checkout.session.completed",
    "identity_source": "metadata.distinct_id"
  }
}
```

One name across every provider, so migrating from Lemon Squeezy to Stripe leaves you with one
continuous history rather than two half-series.

**Amounts are converted to major units.** Every provider sends integer cents; we divide once,
here, and test it per provider. Where a provider reports both gross and net (Polar's `net_amount`,
Dodo's `settlement_amount`) the **net** is used — reporting gross as revenue overstates every
figure by the processor's cut.

### Safety

- **Signature-verified, always.** This endpoint is authenticated by the provider's signature
  rather than your write key, because the provider sends what it sends. With no secret configured
  it returns `501` naming the variable to set, and never accepts unsigned revenue.
- **Replay-protected.** Stripe, Polar and Dodo all sign a timestamp; deliveries more than five
  minutes old are refused. Lemon Squeezy's scheme has no timestamp, so for that one the dedupe
  below is the only replay defence.
- **Retries deduplicate.** The provider's own event id becomes the event id, and the store ignores
  an id it has already seen. Every provider on this list retries; without this, one slow minute
  during a deploy multiplies a customer's revenue in the figure they are most likely to check.

---

## Then what

Once payments are arriving, `payment` is an ordinary metric: it appears in funnels, retention,
breakdowns and the investigation. Findings about it are priced in money automatically, and the
morning brief starts leading with the expensive thing rather than the loud one.

```sh
curl -H "Authorization: Bearer $KEY" "$HOST/v1/trends?event=payment"
curl -H "Authorization: Bearer $KEY" "$HOST/v1/breakdown?event=payment&property=plan"
```

Or ask your editor: **"what's revenue per person by plan?"**
