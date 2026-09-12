package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestReceiptDecision(t *testing.T) {
	tests := []struct {
		name  string
		asset Asset
		ready bool
	}{
		{name: "delivered rendition", asset: Asset{Title: "Episode 7", Ingested: true, ProcessingDone: true, Delivered: true}, ready: true},
		{name: "processing queued", asset: Asset{Title: "Episode 7", Ingested: true, Delivered: true}, ready: false},
		{name: "creator delivery pending", asset: Asset{Title: "Episode 7", Ingested: true, ProcessingDone: true}, ready: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := ReceiptOrder{ID: "ord-204", Email: "creator@example.com", TotalCents: 1299, Assets: []Asset{tt.asset}}
			if got := order.readyForReceipt(); got != tt.ready {
				t.Fatalf("readyForReceipt() = %v, want %v", got, tt.ready)
			}
		})
	}
}

func TestSendRetriesRateLimitWithSameIdempotencyKey(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := req.Header.Get("Idempotency-Key"); got != "receipt-ord-204" {
			t.Fatalf("Idempotency-Key = %q", got)
		}
		if calls == 1 {
			return response(http.StatusTooManyRequests, `{"ok":false,"data":{},"error":{"code":"rate_limited","message":"retry later"},"metadata":{}}`, "0"), nil
		}
		return response(http.StatusOK, `{"ok":true,"data":{"message_id":"msg_204"},"metadata":{}}`, ""), nil
	})}

	sender := ReceiptSender{
		APIKey:     "test-key",
		HTTPClient: client,
		Sleep:      func(context.Context, time.Duration) error { return nil },
	}
	order := ReceiptOrder{
		ID: "ord-204", CreatorName: "Mina", Email: "creator@example.com", TotalCents: 1299,
		Assets: []Asset{{Title: "Episode 7", Ingested: true, ProcessingDone: true, Delivered: true}},
	}
	messageID, err := sender.Send(context.Background(), order)
	if err != nil {
		t.Fatal(err)
	}
	if messageID != "msg_204" || calls != 2 {
		t.Fatalf("messageID = %q, calls = %d", messageID, calls)
	}
}

func response(status int, body, retryAfter string) *http.Response {
	header := make(http.Header)
	if retryAfter != "" {
		header.Set("Retry-After", retryAfter)
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}
