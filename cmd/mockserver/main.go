package main

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

// payloadSnapshot keeps a copy of each request for later inspection.
type payloadSnapshot struct {
	ReceivedAt time.Time       `json:"received_at"`
	Headers    http.Header     `json:"headers"`
	Body       json.RawMessage `json:"body"`
}

func main() {
	addr := flag.String("addr", ":8080", "address for the mock server to listen on")
	path := flag.String("path", "/", "path to accept POSTs on")
	flag.Parse()

	var (
		mu    sync.Mutex
		posts []payloadSnapshot
	)

	http.HandleFunc(*path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("mock server: failed to read body: %v", err)
			http.Error(w, "failed to read body", http.StatusInternalServerError)
			return
		}
		defer r.Body.Close()

		snapshot := payloadSnapshot{
			ReceivedAt: time.Now().UTC(),
			Headers:    r.Header.Clone(),
			Body:       append(json.RawMessage(nil), body...),
		}

		mu.Lock()
		posts = append(posts, snapshot)
		mu.Unlock()

		log.Printf("mock server received payload at %s: %s", snapshot.ReceivedAt.Format(time.RFC3339Nano), string(body))
		w.WriteHeader(http.StatusOK)
	})

	http.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(posts); err != nil {
			log.Printf("mock server: failed to encode events: %v", err)
		}
	})

	log.Printf("mock server listening on %s (POST %s, GET /events)", *addr, *path)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
