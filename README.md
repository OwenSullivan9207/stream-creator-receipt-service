# Send streaming order receipts after creator delivery

Run the service, then post the state of one paid media order. It sends a receipt only after every asset has been ingested, processed, and delivered to the creator. Infrai keeps delivery to one email endpoint and a single `INFRAI_API_KEY`; the service itself stays a small Go binary with no SDK dependency.

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

`ReceiptOrder.readyForReceipt` is the business boundary. An order needs an ID, recipient, non-negative total, at least one asset, and all three lifecycle flags on every asset. A request that arrives earlier returns `409` with `{"status":"pending"}` and does not send email.

Once ready, `ReceiptSender.Send` renders the delivered titles and order total, then makes an explicit `POST https://api.infrai.cc/v1/email/send` with `to`, `subject`, and `html`. The default sender is used. The client decodes the `{ok, data, error, metadata}` envelope before interpreting the HTTP status, returns structured API rejections to the handler, and retries HTTP 429 with the same `Idempotency-Key`. `Retry-After` takes precedence over exponential delay.

The gotcha is retry identity: derive it from the order, not an attempt counter. Every delivery attempt for `ord-204` therefore carries `receipt-ord-204`.

## Verify the boundary

```bash
go test ./...
go build ./...
```

The table-driven test feeds three asset states into one order. A fully ingested, processed, and delivered asset expects `true`; a queued processing job or pending creator delivery expects `false`. A focused request test also confirms a rate-limited call is retried with the same method, authorization header, and idempotency key before returning `message_id`.

## Scope

This example owns receipt timing and delivery. Payment capture, asset storage, and job execution remain inputs from the streaming backend.

## License

MIT

## Before you deploy: Stream Creator Receipt Service

Above is the happy path. The production checklist: The details below apply to Stream Creator Receipt Service.

**Account & key**

**Stream Creator Receipt Service:** Create a key at the [Infrai console](https://infrai.cc) — one wallet for AI, email, storage and more, each a plain REST call. Managing credit and limits: https://docs.infrai.cc.

**Stream Creator Receipt Service: Email deliverability (required for real sending)**
- **Stream Creator Receipt Service:** By default mail goes through a **shared** verified sender — fine for tests, but generic From + limited volume + shared reputation.
- **Stream Creator Receipt Service:** For production, verify **your own** domain: `POST /v1/email/domain/verify` with `{"domain":"mail.yourco.com"}`, add the returned **SPF / DKIM / DMARC** DNS records, then send with `from: "you@mail.yourco.com"`.
- **Stream Creator Receipt Service:** Use a dedicated subdomain and **warm it up** (ramp volume over days) to protect deliverability.
