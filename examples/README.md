# Examples

Start a server first:

```sh
docker run -p 8080:8080 ghcr.io/arjun0606/smolanalytics
# or: smolanalytics serve
```

- **`quickstart.html`** — open it in a browser; it loads the SDK from your server and fires `signup` / `activate` / `checkout` events. Watch them land on the dashboard's **Live events** feed.
- **`send.sh`** — send events from the shell with `curl` (single + batch).

Then open http://localhost:8080 and ask the built-in bar — or connect your own AI:

```sh
claude mcp add --transport http smolanalytics http://localhost:8080/mcp
```
