# F2 test result: the per-PR claim does not work

**Run 2026-08-15 over 210 merged PRs from three real repositories.**

| corpus | n | named metric | + touches instrumented | + user-facing language |
|---|---|---|---|---|
| dub (product) | 70 | 0.0% | 0.0% | 12.9% |
| formbricks (product) | 70 | 0.0% | 11.4% | 15.7% |
| next.js (infra control) | 70 | 0.0% | 0.0% | 2.9% |
| **all** | **210** | **0.0%** | **3.8%** | **10.5%** |

The plan said: if the yield is under about 40%, rescope to deploys touching instrumented flows.
It is **3.8%**. That is not a rescope, it is a refutation.

## Why, and why it is not a classifier bug

The ceiling column uses only language in the title and body — no instrumentation detection at all —
so it does not depend on how well the tooling finds tracking calls. Even that upper bound is 10.5%.

Reading the actual titles makes it obvious:

> Handle `charge.dispute.created` webhook · Migrate domains API tests to Playwright · Use
> HttpBaseClient for Ahrefs domain rating · Modal rerendering fix · Don't expire cross-program ban
> risk events · Limit unverified OAuth app installs to the developer or owning workspace

Roughly nine in ten merges are infrastructure, bug fixes, test migrations, API plumbing and
internal correctness. They are not bets on a metric and no amount of language understanding makes
them into one.

And the ~10% that ARE user-facing — "Improve onboarding flow", "Improve the payout flows" — state
no expected effect. There is no intent in the text to extract, because nobody wrote one down.

## What this rules out

Auto-drafting a claim per merge. It would fire on 90% noise, and asking a human to dismiss nine
prompts to keep one is the exact "annoying form that creates more PM work" failure the design was
supposed to avoid.

## What it points at instead

1. **Claim only what someone already declared as a bet.** A person who creates a feature flag or
   starts an experiment has stated intent explicitly. That population is small, high-signal, and
   the machinery already exists — the kill list is most of it.
2. **Invert the unit: adjudicate the METRIC, not the ship.** "Your checkout conversion is
   statistically unchanged across 23 ships in 90 days" needs no claim object at all, is computable
   today, and is a harder sentence to ignore than any per-PR verdict.

The second is the interesting one, because it delivers the ledger's value without the object whose
feasibility just failed.
