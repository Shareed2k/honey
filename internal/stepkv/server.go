// Package stepkv implements a tiny loopback HTTP key/value API backed by an in-memory TTL cache.
// It is intended for optional per-SSH-run scratch state (see honey cue-exec kv_tunnel).
package stepkv

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

const (
	maxKeyLen       = 256
	maxValueLen     = 65536
	maxBodyRead     = maxValueLen + 4096
	defaultTTL      = 30 * time.Minute
	defaultCap      = 50_000
	shutdownTimeout = 5 * time.Second
)

// Session is one loopback HTTP listener + ttlcache instance + auth token.
type Session struct {
	token   string
	baseURL string // http://host:port (loopback)
	ln      net.Listener
	srv     *http.Server
	cache   *ttlcache.Cache[string, string]
	wg      sync.WaitGroup
}

// Start spins a new session listening on loopback (random port).
func Start(ttl time.Duration) (*Session, error) {
	if ttl <= 0 {
		ttl = defaultTTL
	}
	tok := make([]byte, 24)
	if _, err := rand.Read(tok); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(tok)

	cache := ttlcache.New[string, string](
		ttlcache.WithTTL[string, string](ttl),
		ttlcache.WithCapacity[string, string](defaultCap),
	)
	go cache.Start()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cache.Stop()
		return nil, err
	}
	addr := ln.Addr().String()
	baseURL := "http://" + addr

	mux := http.NewServeMux()
	s := &Session{
		token:   token,
		baseURL: baseURL,
		ln:      ln,
		cache:   cache,
	}
	mux.HandleFunc("/v1/kv/", s.handleKV)

	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		_ = s.srv.Serve(ln)
	}()

	return s, nil
}

// Token returns the bearer token required on requests.
func (s *Session) Token() string { return s.token }

// LocalBaseURL is the operator-side URL (loopback) for this session.
func (s *Session) LocalBaseURL() string { return s.baseURL }

// LocalTCPAddr returns host:port for dialing the loopback listener (no scheme).
func (s *Session) LocalTCPAddr() string {
	if s == nil || s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

func (s *Session) handleKV(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/v1/kv/")
	key = strings.TrimSpace(key)
	if key == "" || strings.Contains(key, "/") {
		http.Error(w, "bad key", http.StatusBadRequest)
		return
	}
	if len(key) > maxKeyLen {
		http.Error(w, "key too long", http.StatusBadRequest)
		return
	}

	// Reserved path documented for cue-exec / k8s kv_tunnel (matches k8s_kv_pod_server.py).
	if key == "__health" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	switch r.Method {
	case http.MethodGet:
		item := s.cache.Get(key)
		if item == nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, item.Value())
	case http.MethodPut:
		b, err := io.ReadAll(io.LimitReader(r.Body, maxBodyRead))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		if len(b) > maxValueLen {
			http.Error(w, "value too long", http.StatusBadRequest)
			return
		}
		s.cache.Set(key, string(b), ttlcache.DefaultTTL)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		s.cache.Delete(key)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Session) authorize(r *http.Request) bool {
	want := []byte(s.token)
	if h := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(strings.ToLower(h), "bearer ") {
		got := strings.TrimSpace(h[7:])
		if len(got) != len(s.token) {
			return false
		}
		return subtle.ConstantTimeCompare([]byte(got), want) == 1
	}
	got := strings.TrimSpace(r.Header.Get("X-Honey-Kv-Token"))
	if len(got) != len(s.token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), want) == 1
}

// Close shuts down the HTTP server and cache.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	var firstErr error
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if s.srv != nil {
		if err := s.srv.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			firstErr = err
		}
	}
	if s.cache != nil {
		s.cache.Stop()
	}
	s.wg.Wait()
	return firstErr
}
