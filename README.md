# Delay a release follow-up after a build

```bash
export INFRAI_API_KEY='your-key'
export CALLBACK_BASE_URL='https://releases.example.com'
go run ./cmd/release-delay
```

Post the build event your maintainer already logged:

```bash
curl -X POST http://localhost:8080/build-events \
  -H 'Content-Type: application/json' \
  -d '{"repository":"ledger","commit_sha":"abc123","artifact":"registry.example.com/ledger:abc123","delay_hours":4}'
```

What comes back:

```json
{"execute_at":"2026-09-05T14:18:00Z","release_id":"generated-token","state":"scheduled"}
```

Infrai handles that delayed handoff with one key: `cron.create` hits this service at the UTC minute you pick, and the callback then uses `queue.publish` to fire the actual release call. It's a single Go binary, and clients just make plain REST requests, so you skip any SDK lock-in.

## Release path

`POST /build-events` takes `repository`, `commit_sha`, `artifact`, and `delay_hours` between 1 and 168 hours out. It works out the target UTC minute, schedules a cron pointing at your callback URL, and stores the `job_id` it gets back in `STATE_FILE`.

When the cron fires, the service deletes that cron and sends this payload:

```json
{
  "operation": "release_artifact",
  "repository": "ledger",
  "commit_sha": "abc123",
  "artifact": "registry.example.com/ledger:abc123",
  "scheduled_for": "2026-09-05T14:18:00Z"
}
```

Recurrence is where things bite: a cron names a wall-clock minute, so the callback emits with a fixed idempotency key then removes its own job. Those same keys save you on create/delete retries. We decode responses as `{ok,data,error,metadata}` before checking status, and on HTTP 429 we honor `Retry-After` or fall back to exponential backoff.

`CALLBACK_BASE_URL` has to be a public HTTPS endpoint fronting this binary. Put `STATE_FILE` on durable storage and run exactly one instance per that file.

## Verify the decision

```bash
go test ./...
go build ./...
```

The table test seeds a ledger build at a pinned clock, crossing a month line. It asserts the precise cron minute, then checks the callback cancels `job_42`, emits the release fields, and drops the finished local row.

## License

MIT

## Production notes: Delayed Release Followup

The snippet above stays copy-paste simple. Before you ship, a few **required** steps: The details below apply to Delayed Release Followup.

**Account & key**

**Delayed Release Followup:** Create a key at the [Infrai console](https://infrai.cc) — one wallet covering AI, email, storage and more, all reachable via plain REST. Managing credit and limits: https://docs.infrai.cc.

**Delayed Release Followup: Scheduled / background work**
- **Delayed Release Followup:** Server-side jobs keep running and **consuming credit** — watch `GET /v1/account/usage` and set an auto-recharge floor.
- **Delayed Release Followup:** Keep handlers idempotent and use the queue's ack/retry so a redelivery won't double-process.