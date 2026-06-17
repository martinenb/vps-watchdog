package web

import (
	"context"
	"embed"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"vps-watchdog/internal/action"
	"vps-watchdog/internal/config"
	"vps-watchdog/internal/db"
	"vps-watchdog/internal/report"
)

//go:embed static
var staticFiles embed.FS

// SSEHub manages Server-Sent Events connections.
type SSEHub struct {
	clients   map[chan string]struct{}
	mu        sync.RWMutex
	broadcast chan string
}

// newSSEHub creates and returns a new SSEHub.
func newSSEHub() *SSEHub {
	return &SSEHub{
		clients:   make(map[chan string]struct{}),
		broadcast: make(chan string, 64),
	}
}

// Register adds a new client channel.
func (h *SSEHub) Register(ch chan string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[ch] = struct{}{}
}

// Unregister removes a client channel.
func (h *SSEHub) Unregister(ch chan string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, ch)
}

// Broadcast sends a message to the broadcast channel for delivery to all clients.
func (h *SSEHub) Broadcast(data string) {
	select {
	case h.broadcast <- data:
	default:
		// Drop if channel full.
	}
}

// Run processes broadcasts and distributes them to registered clients.
// This must be called in a dedicated goroutine.
func (h *SSEHub) Run() {
	for msg := range h.broadcast {
		h.mu.RLock()
		for ch := range h.clients {
			select {
			case ch <- msg:
			default:
				// Slow client — skip.
			}
		}
		h.mu.RUnlock()
	}
}

// Server is the HTTP server for the web dashboard.
type Server struct {
	cfg       *config.Config
	db        *db.DB
	engine    *action.Engine
	scheduler *report.WeeklyScheduler
	graphs    *report.GraphBuilder
	hub       *SSEHub
	srv       *http.Server
}

// New creates a new Server.
func New(cfg *config.Config, database *db.DB, engine *action.Engine, scheduler *report.WeeklyScheduler, graphs *report.GraphBuilder) *Server {
	hub := newSSEHub()
	s := &Server{
		cfg:       cfg,
		db:        database,
		engine:    engine,
		scheduler: scheduler,
		graphs:    graphs,
		hub:       hub,
	}
	return s
}

// Start starts the HTTP server. This call blocks until Stop() is called or an error occurs.
func (s *Server) Start() error {
	go s.hub.Run()

	mux := s.buildRoutes()

	addr := fmt.Sprintf(":%d", s.cfg.Web.Port)
	s.srv = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // 0 for SSE streaming
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("web: listening on %s", addr)
	if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("web: server error: %w", err)
	}
	return nil
}

// Stop gracefully shuts down the HTTP server.
func (s *Server) Stop() {
	if s.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.srv.Shutdown(ctx)
	}
}

// Hub returns the SSE hub for the collection loop to broadcast metrics.
func (s *Server) Hub() *SSEHub {
	return s.hub
}

// basicAuth is middleware that enforces HTTP Basic authentication.
func (s *Server) basicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != s.cfg.Web.Username || pass != s.cfg.Web.Password {
			w.Header().Set("WWW-Authenticate", `Basic realm="VPS Watchdog"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
