// Package main implements a mock ENC / report / PuppetDB receiver for E2E tests.
// It is a simple HTTP server using only the Go standard library.
package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type storedReport struct {
	ReceivedAt time.Time       `json:"received_at"`
	Body       json.RawMessage `json:"body"`
}

type storedPDBCommand struct {
	ReceivedAt time.Time       `json:"received_at"`
	Body       json.RawMessage `json:"body"`
}

type storedClassification struct {
	Certname string    `json:"certname"`
	ServedAt time.Time `json:"served_at"`
}

type server struct {
	mu              sync.Mutex
	reports         []storedReport
	pdbCommands     []storedPDBCommand
	classifications []storedClassification

	encClasses     []string
	encEnvironment string
}

func main() {
	listen := os.Getenv("LISTEN")
	if listen == "" {
		listen = ":8080"
	}

	encClasses := os.Getenv("ENC_CLASSES")
	encEnvironment := os.Getenv("ENC_ENVIRONMENT")

	s := &server{
		encEnvironment: encEnvironment,
	}
	if encClasses != "" {
		for _, c := range strings.Split(encClasses, ",") {
			c = strings.TrimSpace(c)
			if c != "" {
				s.encClasses = append(s.encClasses, c)
			}
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /node/{certname}", s.handleENC)
	mux.HandleFunc("POST /reports", s.handleReport)
	mux.HandleFunc("POST /pdb/cmd/v1", s.handlePDBCommand)
	mux.HandleFunc("GET /api/reports", s.handleAPIReports)
	mux.HandleFunc("GET /api/pdb-commands", s.handleAPIPDBCommands)
	mux.HandleFunc("GET /api/classifications", s.handleAPIClassifications)
	mux.HandleFunc("GET /healthz", s.handleHealthz)

	log.Printf("openvox-mock listening on %s", listen)
	log.Fatal(http.ListenAndServe(listen, mux))
}

func (s *server) handleENC(w http.ResponseWriter, r *http.Request) {
	certname := r.PathValue("certname")
	log.Printf("ENC request for certname=%s", certname)

	s.mu.Lock()
	s.classifications = append(s.classifications, storedClassification{
		Certname: certname,
		ServedAt: time.Now(),
	})
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/x-yaml")
	_, _ = w.Write([]byte("---\n"))
	if s.encEnvironment != "" {
		_, _ = w.Write([]byte("environment: " + s.encEnvironment + "\n"))
	}
	if len(s.encClasses) > 0 {
		_, _ = w.Write([]byte("classes:\n"))
		for _, c := range s.encClasses {
			_, _ = w.Write([]byte("  " + c + ":\n"))
		}
	}
}

func (s *server) handleReport(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	log.Printf("Received report (%d bytes)", len(body))

	s.mu.Lock()
	s.reports = append(s.reports, storedReport{
		ReceivedAt: time.Now(),
		Body:       json.RawMessage(body),
	})
	s.mu.Unlock()

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *server) handlePDBCommand(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	log.Printf("Received PDB command (%d bytes)", len(body))

	s.mu.Lock()
	s.pdbCommands = append(s.pdbCommands, storedPDBCommand{
		ReceivedAt: time.Now(),
		Body:       json.RawMessage(body),
	})
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"uuid":"mock-uuid"}`))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}

func (s *server) handleAPIReports(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, s.reports)
}

func (s *server) handleAPIPDBCommands(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, s.pdbCommands)
}

func (s *server) handleAPIClassifications(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, s.classifications)
}

func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
