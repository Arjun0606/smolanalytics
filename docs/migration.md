# Migration: bring your history with you

Switching analytics tools usually means your dashboard resets to zero. It doesn't
have to. `POST /v1/events` accepts historical timestamps, so anything you can
export as per-event data replays into smolanalytics with the original dates
intact. The `import` subcommand does the mapping, batching and validation:

```sh
smolanalytics import --format=jsonl|csv|posthog|umami \
  --host=http://localhost:8080 --key=$WRITE_KEY FILE
```

Run it with `--dry-run` first: it parses and validates the whole file, prints the
summary and the first 3 mapped events, and sends nothing.

How it behaves:

- Events go up in batches of up to 5000 per request, with a progress line per batch.
- Original timestamps are preserved (that's the whole point).
- Malformed rows are skipped and counted per reason. One bad row never aborts the
  import; the final summary shows parsed / skipped (with why) / sent.

## From another smolanalytics instance (`--format=jsonl`)

Export from the old instance, import into the new one:

```sh
curl -H "Authorization: Bearer $OLD_KEY" \
  "https://old-host/v1/export?format=jsonl" -o events.jsonl
smolanalytics import --format=jsonl --host=https://new-host --key=$NEW_KEY events.jsonl
```

Event ids are preserved and the server dedupes on id, so re-running the same
import is safe. This is also the restore path for the nightly-export backups in
the README.

## From PostHog (`--format=posthog`)

Where the export lives: in PostHog, open **Activity** (the raw events list), set
the date range and any filters, then use the **Export** button and pick CSV. Large
projects may need several date-ranged exports; the importer is happy to run once
per file.

```sh
smolanalytics import --format=posthog --host=https://your-host --key=$KEY posthog-events.csv
```

Mapping: `event` becomes the event name, `distinct_id` stays `distinct_id`,
`timestamp` is preserved. Properties are handled in both shapes PostHog exports:
an embedded-JSON `properties` column, or flattened `properties.$browser`-style
columns (the prefix is stripped). When both carry the same key, the JSON column
wins because it keeps the original types.

## From Umami (`--format=umami`)

Where the export lives: in Umami, **Settings → Websites → your website → Data →
Export** produces a CSV of the `website_event` table (Umami Cloud emails you a
download link). Self-hosted with database access can also dump the table
directly:

```sql
\copy (SELECT * FROM website_event WHERE website_id = '...') TO 'umami.csv' CSV HEADER
```

```sh
smolanalytics import --format=umami --host=https://your-host --key=$KEY umami.csv
```

Mapping: rows with an empty `event_name` are pageviews and become `$pageview`
with `url_path` as the `path` property, which is exactly what the web view (top
pages, referrers, devices) reads. Custom events keep their `event_name`.
`created_at` is the timestamp and `session_id` becomes the `distinct_id`.

One honest caveat: Umami has no stable cross-session visitor id, so each session
imports as its own "user". Traffic, pages and event trends carry over cleanly;
retention and user-level reports can only be as good as the source data, and
Umami never had that linkage.

## From Plausible: read this first

Plausible's export (**Site Settings → Imports & Exports → Export data**) is a set
of **aggregated CSVs**: visitors per day, top pages, top sources. There is no
per-event history in it, and aggregates cannot be turned back into events, so
there is nothing for the importer to replay. **You start fresh.** Keep the old
Plausible dashboard around for reference; your smolanalytics history begins the
day you switch. This is not an importer limitation, the per-event data was never
exportable from Plausible in the first place.

## From GA4: read this first

GA4's UI exports are aggregated reports, same problem as Plausible: aggregates
cannot become events. GA4 *can* produce raw events, but only through the
**BigQuery link** (GA4 Admin → Product links → BigQuery links), and only from the
day you enabled it. If you had it enabled, flatten the events into the generic
CSV shape and import with `--format=csv`:

```sql
-- BigQuery: flatten GA4 raw events into the generic import shape
SELECT
  event_name AS name,
  user_pseudo_id AS distinct_id,
  FORMAT_TIMESTAMP('%FT%TZ', TIMESTAMP_MICROS(event_timestamp)) AS time,
  (SELECT value.string_value FROM UNNEST(event_params) WHERE key = 'page_location') AS path
FROM `your-project.analytics_XXXXXXX.events_*`
```

Export the result as CSV, then:

```sh
smolanalytics import --format=csv --host=https://your-host --key=$KEY ga4.csv
```

If BigQuery was never linked, the raw events don't exist anywhere (GA4 does not
backfill) and you start fresh from today.

## Generic CSV (`--format=csv`)

For everything else. Header row required, then:

| column | required | notes |
| --- | --- | --- |
| `name` (or `event`) | yes | the event name |
| `distinct_id` (or `user_id` / `anonymous_id`) | yes | who did it |
| `time` (or `timestamp`) | no | RFC3339, `YYYY-MM-DD HH:MM:SS`, or unix seconds/millis; missing means "now" |
| everything else | no | becomes a string property under its column name |
