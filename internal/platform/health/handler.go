package health

import (
	"encoding/json"
	"net/http"
)

func Handler(service *Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(writer http.ResponseWriter, _ *http.Request) {
		snapshot := service.Snapshot()
		status := http.StatusOK
		if !snapshot.Live {
			status = http.StatusServiceUnavailable
		}
		write(writer, snapshot, status)
	})
	mux.HandleFunc("GET /health/ready", func(writer http.ResponseWriter, _ *http.Request) {
		snapshot := service.Snapshot()
		status := http.StatusOK
		if !snapshot.Ready {
			status = http.StatusServiceUnavailable
		}
		write(writer, snapshot, status)
	})
	return mux
}

func write(writer http.ResponseWriter, value any, status int) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
