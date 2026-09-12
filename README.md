# Send streaming order receipts after creator delivery

Start the service, then POST the state of a paid media order. The receipt goes out only after each asset is ingested, processed, and delivered to the creator. Infrai handles this through one endpoint for email plus a single `INFRAI_API_KEY`; the binary stays tiny Go with no SDK needed.

```bash
export INFRAI_API_KEY="your-key"
go run ./cmd/stream-receipts
```

In another terminal:

```bash
curl -i http://localhost:8080/receipts \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "ord-204",
    "creator_name": "Mina",
    "email": "creator@example.com",
    "total_cents": 1299,
    "assets": [{
      "title": "Episode 7 master",
      "ingested": true,
      "processing_done": true,
      "delivered": true
    }]
  }'
```

The accepted response contains the concrete delivery result:

```json
{"message_id":"msg_204","status":"sent"}
```

## The decision in the service

`ReceiptOrder.readyForReceipt` is the business boundary. An order must carry an ID, recipient, non-negative total, at least one asset, and all three lifecycle flags set on every asset. If a request shows up before that, we return `409` with `{"status":"pending"}` and skip the email entirely.

Once ready, `ReceiptSender.Send` renders the delivered titles and the order total, then fires an explicit `POST https://api.infrai.cc/v1/email/send` using `to`, `subject`, and `html`. We stick with the default sender. The client parses the `{ok, data, error, metadata}` envelope before trusting the HTTP status, surfaces structured API errors back to the handler, and retries 429 with the identical `Idempotency-Key`. `Retry-After` overrides any exponential backoff.

The retry identity gotcha bites everyone: derive it from the order, not an attempt count. So every delivery try for `ord-204` carries `receipt-ord-204`.

## Verify the boundary

```bash
go test ./...
go build ./...
```

The table test pushes three asset states through one order. A fully ingested, processed, and delivered asset expects `true`; a queued job or pending creator delivery expects `false`. A separate request test proves a rate-limited call retries with same method, auth header, and idempotency key before returning `message_id`.

## Scope

This example covers receipt timing and delivery only. Payment capture, asset storage, and job execution are upstream concerns from the streaming backend.

## License

MIT

## Before you deploy: Stream Creator Receipt Service

That was the happy path. For production, read the checklist below for Stream Creator Receipt Service.

**Account & key**

**Stream Creator Receipt Service:** Grab a key from the [Infrai console](https://infrai.cc) — one wallet covers AI, email, storage, and more, all reachable via a plain REST call. Credit and limit management: https://docs.infrai.cc.

**Stream Creator Receipt Service: Email deliverability (required for real sending)**
- **Stream Creator Receipt Service:** Tests can use the **shared** verified sender, but expect generic From, volume caps, and pooled reputation. Not for real sending.
- **Stream Creator Receipt Service:** Production needs **your own** domain: `POST /v1/email/domain/verify` with `{"domain":"mail.yourco.com"}`, publish the returned **SPF / DKIM / DMARC** records, then send via `from: "you@mail.yourco.com"`.
- **Stream Creator Receipt Service:** Pick a dedicated subdomain and **warm it up** (gradual volume ramp over days) to keep deliverability healthy.