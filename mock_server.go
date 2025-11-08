package main

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

/*
Run:
  go run mock_server.go

Endpoints:
  POST /report   (accepts JSON: { "prefix": "...", "count": N, "order_ids": ["..."] })
  GET  /report   (returns latest JSON)
  GET  /         (HTML view of latest report)
  POST /reset    (clear report)
*/

type Report struct {
	Prefix     string    `json:"prefix"`
	Count      int       `json:"count"`
	OrderIDs   []string  `json:"order_ids"`
	ReceivedAt time.Time `json:"received_at"`
}

var (
	mu     sync.RWMutex
	latest *Report
)

func main() {
	http.HandleFunc("/", handleHTML)
	http.HandleFunc("/report", handleReport)
	http.HandleFunc("/reset", handleReset)

	log.Println("Mock server listening on :9001")
	log.Fatal(http.ListenAndServe(":9001", nil))
}

func handleReport(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var rep Report
		if err := json.NewDecoder(r.Body).Decode(&rep); err != nil {
			http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
			return
		}
		rep.Prefix = strings.ToLower(strings.TrimSpace(rep.Prefix))
		rep.ReceivedAt = time.Now().UTC()

		// sanity: if count mismatches length, trust the list and fix count
		if rep.Count != len(rep.OrderIDs) {
			rep.Count = len(rep.OrderIDs)
		}

		mu.Lock()
		latest = &rep
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"saved":  rep.Count,
		})
	case http.MethodGet:
		mu.RLock()
		defer mu.RUnlock()
		if latest == nil {
			http.Error(w, "no report yet", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(latest)
	default:
		http.Error(w, "use GET or POST", http.StatusMethodNotAllowed)
	}
}

func handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	mu.Lock()
	latest = nil
	mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

var htmlTpl = template.Must(template.New("view").Parse(`
<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>Completed Orders (Mock)</title>
<style>
 body { font-family: system-ui, sans-serif; margin: 2rem; }
 .card { max-width: 900px; padding: 1rem 1.25rem; border: 1px solid #e3e3e3; border-radius: 12px; }
 .muted { color: #666; }
 code { background: #f6f6f6; padding: 0 .25rem; border-radius: 4px; }
 ul { line-height: 1.7; }
</style>
</head>
<body>
<h2>Completed Orders Summary</h2>
<div class="card">
{{ if . }}
  <p><strong>Prefix:</strong> <code>{{ .Prefix }}</code></p>
  <p><strong>Count:</strong> {{ .Count }}</p>
  <p class="muted">Last updated: {{ .ReceivedAt }}</p>
  <h3>Order IDs</h3>
  {{ if .OrderIDs }}
    <ul>
      {{ range .OrderIDs }}
        <li><code>{{ . }}</code></li>
      {{ end }}
    </ul>
  {{ else }}
    <p>(none)</p>
  {{ end }}
{{ else }}
  <p>No report received yet. POST JSON to <code>/report</code>.</p>
{{ end }}
</div>
</body>
</html>
`))

func handleHTML(w http.ResponseWriter, r *http.Request) {
	mu.RLock()
	defer mu.RUnlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = htmlTpl.Execute(w, latest)
}
