package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
)

func main() {
	apiKey := os.Getenv("INFRAI_API_KEY")
	if apiKey == "" {
		log.Fatal("INFRAI_API_KEY is required")
	}
	sender := ReceiptSender{APIKey: apiKey}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /receipts", func(w http.ResponseWriter, r *http.Request) {
		var order ReceiptOrder
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&order); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid order"})
			return
		}
		if !order.readyForReceipt() {
			writeJSON(w, http.StatusConflict, map[string]string{"status": "pending"})
			return
		}
		messageID, err := sender.Send(r.Context(), order)
		if err != nil {
			var apiErr *InfraiError
			if errors.As(err, &apiErr) && apiErr.Status >= 400 && apiErr.Status < 500 {
				writeJSON(w, apiErr.Status, map[string]string{"error": apiErr.Code})
				return
			}
			log.Printf("send receipt: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "delivery_failed"})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "sent", "message_id": messageID})
	})

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("stream receipt service listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write response: %v", err)
	}
}
