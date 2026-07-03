# Tracking that can't silently break

Instrumentation fails quietly. A refactor renames a component, a handler gets deleted,
and an event you chart every week stops arriving. Nothing errors. You find out weeks
later, when the data you need has a hole in it.

smolanalytics closes that gap with a tracking plan that lives in your repo:
`smolanalytics.plan.json`, committed next to the code that implements it.

## Why a file in the repo

- **Code-reviewed.** Adding an event means editing the plan in the same PR as the
  `track()` call. Reviewers see intent and implementation together, and `git blame`
  answers "who added this event and why".
- **Agent-readable.** Your coding agent reads the plan like any other file, so it
  knows what the app is supposed to track before it touches the code. The running
  instance serves the same plan over MCP (`instrumentation_health`), so the agent,
  the CLI, and CI all verify against one source of truth.
- **CI-checkable.** `plan check` exits 1 when a planned event stopped arriving or
  lost an expected property. A failing job, not a hole you find in a chart.

## The three commands

```sh
smolanalytics plan init     # write a starter smolanalytics.plan.json — edit, commit
smolanalytics plan push     # declare the file's plan on your running instance
smolanalytics plan check    # verify real traffic matches; exit 1 if not
```

(`plan pull` also exists: it writes the instance's current plan back into the file,
for adopting this workflow on an instance that already has a plan.)

All of them talk to a running instance over its MCP endpoint (`POST /mcp`), the same
path a connected coding agent uses. Point them at production with `--host` and an API
key (created in Settings or via the `create_api_key` tool):

```sh
smolanalytics plan push --host=https://analytics.example.com --key=$KEY
smolanalytics plan check --host=https://analytics.example.com --key=$KEY --window=24
```

The file's shape, exactly what `plan init` writes:

```json
{
  "events": [
    { "name": "signup", "description": "account created", "properties": ["plan"] }
  ]
}
```

`plan check` renders one line per planned event: `✓` it is flowing, `✗` it never
arrived or arrives without an expected property. Events seen in traffic but absent
from the plan are listed as informational only; they never fail the check, because a
gate that punishes adding tracking teaches people to stop adding tracking.

```
tracking plan: 3 events declared

  ✓ signup       1035 events · last seen 2026-07-03T23:44:00Z
  ✗ checkout     flowing (309 events) but missing properties: amount
  ✗ invite_sent  planned but never arrived
  • activate — seen but not in the plan (informational)
```

## The CI job

Copy-paste, then set two repository secrets: `SMOLANALYTICS_HOST` (your instance URL)
and `SMOLANALYTICS_KEY` (an API key).

```yaml
name: tracking-plan
on:
  schedule:
    - cron: "17 6 * * *"     # nightly, after a day of real traffic
  workflow_run:              # and once after each production deploy
    workflows: ["deploy"]    # <- your deploy workflow's name
    types: [completed]
  workflow_dispatch: {}      # run it by hand any time

jobs:
  plan-check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Install smolanalytics
        run: |
          curl -fsSL https://raw.githubusercontent.com/Arjun0606/smolanalytics/main/install.sh | PREFIX="$HOME/.local/bin" sh
          echo "$HOME/.local/bin" >> "$GITHUB_PATH"
      - name: Push the repo's plan
        run: smolanalytics plan push --host="$SMOLANALYTICS_HOST" --key="$SMOLANALYTICS_KEY"
        env:
          SMOLANALYTICS_HOST: ${{ secrets.SMOLANALYTICS_HOST }}
          SMOLANALYTICS_KEY: ${{ secrets.SMOLANALYTICS_KEY }}
      - name: Verify real traffic matches the plan
        run: smolanalytics plan check --host="$SMOLANALYTICS_HOST" --key="$SMOLANALYTICS_KEY" --window=24
        env:
          SMOLANALYTICS_HOST: ${{ secrets.SMOLANALYTICS_HOST }}
          SMOLANALYTICS_KEY: ${{ secrets.SMOLANALYTICS_KEY }}
```

Pushing before checking keeps the instance's plan in lockstep with the default
branch, so the check always verifies against what the repo currently intends.

## When to run it, honestly

`plan check` verifies real traffic, not code. It can only flag a broken event after
enough time has passed for that event to have plausibly fired. Run it per-commit and
it fails on every quiet hour and every event users trigger a few times a day; that is
noise, and noisy gates get deleted.

Nightly with `--window=24` is the sane default: a full day of traffic, and a break
ships at most one day before you know. The on-deploy run above is an early warning
for your high-traffic events; expect it to be inconclusive for rare ones. If some
events fire weekly (billing renewals, digests), widen the window (`--window=168`) or
keep them out of the plan rather than living with a flaky gate.
