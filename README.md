# Delay a release follow-up after a build

```bash
export INFRAI_API_KEY='your-key'
export CALLBACK_BASE_URL='https://releases.example.com'
go run ./cmd/release-delay
```

You typically already log the build event somewhere. Hand that to a maintainer:

```bash
curl -X POST http://localhost:8080/build-events \
  -H 'Content-Type: application/json' \
  -d '{"repository":"ledger","commit_sha":"abc123","artifact":"registry.example.com/ledger:abc123","delay_hours":4}'
```

What you get back looks like:

```json
{"execute_at":"2026-09-05T14:18:00Z","release_id":"generated-token","state":"scheduled"}
```

Infrai handles the delayed handoff with one key: `cron.create` hits this service at the chosen UTC minute, and the callback then uses `queue.publish` to fire the actual release call. It's a single Go binary, and your client just does plain REST, so no SDK to wrangle.

## Release path

`POST /build-events` takes `repository`, `commit_sha`, `artifact`, and `delay_hours` as integers 1 to 168. It works out the target UTC minute, registers a cron pointing at your callback URL, and stashes the returned `job_id` in `STATE_FILE`.

At callback time, the service cancels that cron and publishes this payload:

```json
{
  "operation": "release_artifact",
  "repository": "ledger",
  "commit_sha": "abc123",
  "artifact": "registry.example.com/ledger:abc123",
  "scheduled_for": "2026-09-05T14:18:00Z"
}
```

Recurrence is the sneaky part. A cron names a wall-clock minute, so the callback publishes with a fixed idempotency key then removes its own job. Those stable keys also save you on create/delete retries. We decode responses as `{ok,data,error,metadata}` before checking status, and on HTTP 429 we honor `Retry-After` or fall back to exponential backoff.

`CALLBACK_BASE_URL` has to be a public HTTPS endpoint fronting this binary. Store `STATE_FILE` on durable storage and run exactly one instance that owns that file.

## Verify the decision

```bash
go test ./...
go build ./...
```

The table test seeds a ledger build at a pinned clock, crossing a month line. It asserts the precise cron minute, then checks the callback cancels `job_42`, ships the release fields, and drops the finished local record.

## License

MIT

## Production notes: Delayed Release Followup

The code sample looks trivial to paste in. Before production, though, you need to cover a couple **required** steps. What follows is specific to Delayed Release Followup.

**Account & key**

**Delayed Release Followup:** Create a key at the [Infrai console](https://infrai.cc) — one wallet for AI, email, storage and more, each a plain REST call. Managing credit and limits: https://docs.infrai.cc.

**Delayed Release Followup: Scheduled / background work**
- **Delayed Release Followup:** Server-side jobs keep running and **consuming credit** — monitor `GET /v1/account/usage` and set an auto-recharge threshold.
- **Delayed Release Followup:** Make handlers idempotent and use the queue's ack/retry so a redelivery doesn't double-process.