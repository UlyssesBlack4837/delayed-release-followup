package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"

	"example.com/delayed-release-followup/internal/releaseflow"
)

type server struct {
	scheduler *releaseflow.Scheduler
}

func main() {
	apiKey := requiredEnv("INFRAI_API_KEY")
	callbackURL := requiredEnv("CALLBACK_BASE_URL")
	stateFile := envOr("STATE_FILE", "./var/releases.json")
	address := envOr("LISTEN_ADDR", ":8080")

	s := &server{scheduler: releaseflow.NewScheduler(releaseflow.NewClient(apiKey), releaseflow.NewStore(stateFile), callbackURL)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /build-events", s.schedule)
	mux.HandleFunc("POST /internal/release-followup/{token}", s.dispatch)
	log.Printf("release delay service listening on %s", address)
	log.Fatal(http.ListenAndServe(address, mux))
}

func (s *server) schedule(w http.ResponseWriter, r *http.Request) {
	var build releaseflow.BuildEvent
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&build); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid build event"})
		return
	}
	release, err := s.scheduler.Schedule(r.Context(), build)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"release_id": release.Token,
		"execute_at": release.ExecuteAt,
		"state":      "scheduled",
	})
}

func (s *server) dispatch(w http.ResponseWriter, r *http.Request) {
	if err := s.scheduler.Dispatch(r.Context(), r.PathValue("token")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "release not found"})
			return
		}
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": "queued"})
}

func writeError(w http.ResponseWriter, err error) {
	var apiErr *releaseflow.APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatus >= 400 && apiErr.HTTPStatus < 500 {
		writeJSON(w, apiErr.HTTPStatus, map[string]string{"error": apiErr.Code, "message": apiErr.Message})
		return
	}
	if strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "delay_hours") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("request failed: %v", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "request could not be completed"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write response: %v", err)
	}
}

func requiredEnv(name string) string {
	value := os.Getenv(name)
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
