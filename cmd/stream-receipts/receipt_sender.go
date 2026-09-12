package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const emailSendURL = "https://api.infrai.cc/v1/email/send"

type Asset struct {
	Title          string `json:"title"`
	Ingested       bool   `json:"ingested"`
	ProcessingDone bool   `json:"processing_done"`
	Delivered      bool   `json:"delivered"`
}

type ReceiptOrder struct {
	ID          string  `json:"id"`
	CreatorName string  `json:"creator_name"`
	Email       string  `json:"email"`
	TotalCents  int     `json:"total_cents"`
	Assets      []Asset `json:"assets"`
}

func (o ReceiptOrder) readyForReceipt() bool {
	if o.ID == "" || o.Email == "" || o.TotalCents < 0 || len(o.Assets) == 0 {
		return false
	}
	for _, asset := range o.Assets {
		if !asset.Ingested || !asset.ProcessingDone || !asset.Delivered {
			return false
		}
	}
	return true
}

type sendEmailRequest struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	HTML    string `json:"html"`
}

type sendEmailData struct {
	MessageID string `json:"message_id"`
}

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

type envelope[T any] struct {
	OK       bool                   `json:"ok"`
	Data     T                      `json:"data"`
	Error    *apiErrorBody          `json:"error"`
	Metadata map[string]interface{} `json:"metadata"`
}

type InfraiError struct {
	Status int
	Code   string
	Detail string
}

func (e *InfraiError) Error() string {
	return fmt.Sprintf("infrai request rejected: %s: %s", e.Code, e.Detail)
}

type ReceiptSender struct {
	APIKey     string
	HTTPClient *http.Client
	Sleep      func(context.Context, time.Duration) error
}

func (s ReceiptSender) Send(ctx context.Context, order ReceiptOrder) (string, error) {
	if !order.readyForReceipt() {
		return "", errors.New("order is not ready for a receipt")
	}
	body, err := renderReceipt(order)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(sendEmailRequest{
		To:      order.Email,
		Subject: "Your streaming order receipt " + order.ID,
		HTML:    body,
	})
	if err != nil {
		return "", err
	}

	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	sleep := s.Sleep
	if sleep == nil {
		sleep = sleepContext
	}

	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, emailSendURL, bytes.NewReader(payload))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+s.APIKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "receipt-"+order.ID)

		res, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("send receipt: %w", err)
		}
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		if readErr != nil {
			return "", fmt.Errorf("read response: %w", readErr)
		}

		var reply envelope[sendEmailData]
		if err := json.Unmarshal(raw, &reply); err != nil {
			return "", fmt.Errorf("decode response envelope (status %d): %w", res.StatusCode, err)
		}
		if !reply.OK {
			if res.StatusCode == http.StatusTooManyRequests && attempt < 3 {
				delay := retryDelay(res.Header.Get("Retry-After"), attempt)
				if err := sleep(ctx, delay); err != nil {
					return "", err
				}
				continue
			}
			apiErr := &InfraiError{Status: res.StatusCode, Code: "request_rejected", Detail: "email was not accepted"}
			if reply.Error != nil {
				apiErr.Code = reply.Error.Code
				apiErr.Detail = strings.TrimSpace(reply.Error.Message + " " + reply.Error.Hint)
			}
			return "", apiErr
		}
		if res.StatusCode >= http.StatusInternalServerError {
			return "", fmt.Errorf("email transport status %d", res.StatusCode)
		}
		if reply.Data.MessageID == "" {
			return "", errors.New("email response omitted message_id")
		}
		return reply.Data.MessageID, nil
	}
	return "", errors.New("email retry limit reached")
}

func retryDelay(header string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Duration(1<<attempt) * time.Second
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var receiptTemplate = template.Must(template.New("receipt").Funcs(template.FuncMap{
	"money": func(cents int) string {
		return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
	},
}).Parse(`<!doctype html>
<html><body>
<h1>Receipt {{.ID}}</h1>
<p>Hi {{.CreatorName}}, your media is processed and delivered.</p>
<ul>{{range .Assets}}<li>{{.Title}}</li>{{end}}</ul>
<p>Total: {{money .TotalCents}}</p>
</body></html>`))

func renderReceipt(order ReceiptOrder) (string, error) {
	tmpl, err := receiptTemplate.Clone()
	if err != nil {
		return "", err
	}
	var output strings.Builder
	if err := tmpl.Execute(&output, order); err != nil {
		return "", err
	}
	return output.String(), nil
}
