package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

func main() {
	address := os.Getenv("DENYRA_ACCEPTANCE_FIXTURE_ADDRESS")
	if address == "" {
		address = "0.0.0.0:18080"
	}
	var requests atomic.Uint64
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /health/ready", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]any{"live": true, "ready": true})
	})
	mux.HandleFunc("GET /acceptance/evidence", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]any{"requests": requests.Load(), "fixture": "denyra-local-adapters"})
	})
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		switch request.URL.Path {
		case "/api/v1/system/status":
			writeJSON(writer, map[string]any{"version": "acceptance-fixture"})
		case "/api/v1/wanted/missing", "/api/v1/queue", "/api/v1/history":
			writeJSON(writer, map[string]any{"records": []any{}, "totalRecords": 0})
		case "/api/get", "/api/search":
			writeJSON(writer, map[string]any{"recordings": []any{}, "releases": []any{}})
		default:
			http.Error(writer, "fixture no-result", http.StatusNotFound)
		}
	})
	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}
