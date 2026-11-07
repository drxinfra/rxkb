// Package web serves the MCP endpoint over HTTP with the limits a public,
// unauthenticated service needs: per-address request rate, total concurrency,
// request size and time. It logs one line per request with metadata only: the
// arguments of a tool call (log lines people paste, with whatever they contain)
// are never written anywhere.
package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"
)

// Options are the limits of the endpoint.
type Options struct {
	Rate          rate.Limit // requests per second per client address
	Burst         int        // requests a client may send at once
	MaxConcurrent int        // requests processed at the same time, all clients together
	MaxBody       int64      // bytes of a request body
	Page          string     // human page about the server: browsers opening /mcp go there
	Log           *log.Logger
}

// Handler returns the HTTP handler: /mcp for MCP, /healthz for local checks.
func Handler(current func() *mcp.Server, health func() string, o Options) http.Handler {
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return current() }, &mcp.StreamableHTTPOptions{
		// Sessionless (MCP 2026-07-28): no session state on the server, every POST stands alone.
		// Older clients still initialize; their sessions are temporary per request.
		Stateless:    true,
		JSONResponse: true, // plain JSON answers, no long-lived event streams to hold connections
		// The service is reached only through the reverse proxy, so every request arrives from
		// 127.0.0.1 with a public Host. The protection is meant for local servers (DNS rebinding)
		// and would reject all of them; this server holds no user data or credentials to protect.
		DisableLocalhostProtection:   true,
		MaxRequestBodyBytes:          o.MaxBody,
		PropagateRequestCancellation: true,
	})
	s := &server{o: o, lim: newLimiter(o.Rate, o.Burst), sem: make(chan struct{}, o.MaxConcurrent)}
	mux := http.NewServeMux()
	mux.Handle("/mcp", s.wrap(h))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, health())
	})
	return s.recoverer(mux)
}

type server struct {
	o   Options
	lim *limiter
	sem chan struct{}
}

func (s *server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				// The panic value may quote the request, so only the stack goes to the log.
				s.o.Log.Printf("panic: %s", debug.Stack())
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// clientIP trusts X-Real-IP only from the reverse proxy on the same host.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if x := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(x) != nil {
			return x
		}
	}
	return host
}

func (s *server) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		// Public read-only data without cookies or credentials: any web page may call it.
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Expose-Headers", "Mcp-Session-Id, Mcp-Protocol-Version")
		rw := &recorder{ResponseWriter: w, status: http.StatusOK}
		var info callInfo
		defer func() { s.access(r, rw, info, time.Since(start)) }()

		switch r.Method {
		case http.MethodOptions:
			h.Set("Access-Control-Allow-Methods", "POST, GET, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "*")
			h.Set("Access-Control-Max-Age", "86400")
			rw.WriteHeader(http.StatusNoContent)
			return
		case http.MethodGet:
			// A person who opens the address in a browser gets the page about the server.
			if a := r.Header.Get("Accept"); strings.Contains(a, "text/html") && !strings.Contains(a, "text/event-stream") {
				http.Redirect(rw, r, s.o.Page, http.StatusFound)
				return
			}
		}
		if !s.lim.allow(clientIP(r)) {
			h.Set("Retry-After", "5")
			http.Error(rw, "too many requests", http.StatusTooManyRequests)
			return
		}
		select {
		case s.sem <- struct{}{}:
			defer func() { <-s.sem }()
		case <-time.After(2 * time.Second):
			h.Set("Retry-After", "5")
			http.Error(rw, "server is busy", http.StatusServiceUnavailable)
			return
		case <-r.Context().Done():
			return
		}
		if r.Method == http.MethodPost {
			body, err := io.ReadAll(http.MaxBytesReader(rw, r.Body, s.o.MaxBody))
			if err != nil {
				http.Error(rw, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			info = peek(body)
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		next.ServeHTTP(rw, r)
	})
}

// callInfo is what the access line says about a JSON-RPC request.
type callInfo struct {
	method, tool, client string
	bytes                int
}

// peek reads the method, the tool name and the client name; never the arguments.
func peek(body []byte) callInfo {
	ci := callInfo{bytes: len(body)}
	var m struct {
		Method string `json:"method"`
		Params struct {
			Name       string                         `json:"name"`
			ClientInfo struct{ Name, Version string } `json:"clientInfo"`
			Meta       map[string]json.RawMessage     `json:"_meta"`
		} `json:"params"`
	}
	if len(body) > 0 && body[0] == '[' {
		ci.method = "batch"
		return ci
	}
	if json.Unmarshal(body, &m) != nil {
		ci.method = "invalid"
		return ci
	}
	ci.method, ci.tool = m.Method, m.Params.Name
	ci.client = m.Params.ClientInfo.Name + " " + m.Params.ClientInfo.Version
	for k, v := range m.Params.Meta {
		if strings.HasSuffix(k, "clientInfo") {
			var c struct{ Name, Version string }
			if json.Unmarshal(v, &c) == nil && c.Name != "" {
				ci.client = c.Name + " " + c.Version
			}
		}
	}
	ci.method, ci.tool, ci.client = safe(ci.method, 40), safe(ci.tool, 40), safe(strings.TrimSpace(ci.client), 60)
	return ci
}

// safe keeps a short printable token for the log.
func safe(s string, n int) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= n {
			break
		}
		if unicode.IsPrint(r) && r != '"' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *server) access(r *http.Request, rw *recorder, ci callInfo, d time.Duration) {
	s.o.Log.Printf("%s %s %d %dms in=%d out=%d rpc=%q tool=%q client=%q", r.Method, r.URL.Path, rw.status,
		d.Milliseconds(), ci.bytes, rw.bytes, ci.method, ci.tool, ci.client)
}

type recorder struct {
	http.ResponseWriter
	status, bytes int
	wrote         bool
}

func (r *recorder) WriteHeader(code int) {
	if !r.wrote {
		r.status, r.wrote = code, true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	r.wrote = true
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// limiter is a token bucket per client address with a bounded table.
type limiter struct {
	mu    sync.Mutex
	r     rate.Limit
	b     int
	byIP  map[string]*entry
	clean time.Time
}

type entry struct {
	l    *rate.Limiter
	seen time.Time
}

const maxClients = 50000

func newLimiter(r rate.Limit, b int) *limiter {
	return &limiter{r: r, b: b, byIP: map[string]*entry{}, clean: time.Now()}
}

func (l *limiter) allow(ip string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.clean) > time.Minute {
		for k, e := range l.byIP {
			if now.Sub(e.seen) > 10*time.Minute {
				delete(l.byIP, k)
			}
		}
		l.clean = now
	}
	e := l.byIP[ip]
	if e == nil {
		if len(l.byIP) >= maxClients {
			return false // a flood from many addresses: new ones wait for the cleanup
		}
		e = &entry{l: rate.NewLimiter(l.r, l.b)}
		l.byIP[ip] = e
	}
	e.seen = now
	return e.l.AllowN(now, 1)
}
