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
	healthKey       = "__health"
)

// ErrBadKey is returned when a key is empty, too long, contains '/', or is reserved.
var ErrBadKey = errors.New("stepkv: bad key")

// ErrValueTooLong is returned when a value exceeds maxValueLen.
var ErrValueTooLong = errors.New("stepkv: value too long")

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

func validateKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" || strings.Contains(key, "/") {
		return ErrBadKey
	}
	if len(key) > maxKeyLen {
		return ErrBadKey
	}
	if key == healthKey {
		return ErrBadKey
	}
	return nil
}

// Get returns the value for key and whether it was found.
func (s *Session) Get(key string) (value string, found bool, err error) {
	if s == nil || s.cache == nil {
		return "", false, errors.New("stepkv: nil session")
	}
	if err := validateKey(key); err != nil {
		return "", false, err
	}
	item := s.cache.Get(strings.TrimSpace(key))
	if item == nil {
		return "", false, nil
	}
	return item.Value(), true, nil
}

// Put stores value for key.
func (s *Session) Put(key, value string) error {
	if s == nil || s.cache == nil {
		return errors.New("stepkv: nil session")
	}
	if err := validateKey(key); err != nil {
		return err
	}
	if len(value) > maxValueLen {
		return ErrValueTooLong
	}
	s.cache.Set(strings.TrimSpace(key), value, ttlcache.DefaultTTL)
	return nil
}

// Delete removes key if present.
func (s *Session) Delete(key string) error {
	if s == nil || s.cache == nil {
		return errors.New("stepkv: nil session")
	}
	if err := validateKey(key); err != nil {
		return err
	}
	s.cache.Delete(strings.TrimSpace(key))
	return nil
}

func (s *Session) handleKV(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	key := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/v1/kv/"))
	if key == healthKey {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := validateKey(key); err != nil {
		http.Error(w, "bad key", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		val, found, err := s.Get(key)
		if err != nil {
			http.Error(w, "bad key", http.StatusBadRequest)
			return
		}
		if !found {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = io.WriteString(w, val) // #nosec G705 -- opaque scratch value for curl/automation; not HTML (text/plain + nosniff)
	case http.MethodPut:
		b, err := io.ReadAll(io.LimitReader(r.Body, maxBodyRead))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		if err := s.Put(key, string(b)); err != nil {
			if errors.Is(err, ErrValueTooLong) {
				http.Error(w, "value too long", http.StatusBadRequest)
				return
			}
			http.Error(w, "bad key", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := s.Delete(key); err != nil {
			http.Error(w, "bad key", http.StatusBadRequest)
			return
		}
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
