package web

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drxinfra/rxkb/internal/kb"
	"github.com/drxinfra/rxkb/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"
)

func testServer(t *testing.T, o Options) (*httptest.Server, *bytes.Buffer) {
	t.Helper()
	k, err := kb.LoadFile(filepath.Join("..", "..", "testdata", "kb.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := tools.New(k, "test")
	var logs bytes.Buffer
	o.Log = log.New(&logs, "", 0)
	if o.MaxBody == 0 {
		o.MaxBody = 4096
	}
	if o.MaxConcurrent == 0 {
		o.MaxConcurrent = 4
	}
	if o.Rate == 0 {
		o.Rate, o.Burst = rate.Inf, 1
	}
	o.Page = "https://drxinfra.ru/mcp.html"
	ts := httptest.NewServer(Handler(func() *mcp.Server { return s }, func() string { return "ok" }, o))
	t.Cleanup(ts.Close)
	return ts, &logs
}

func post(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"tester","version":"9"}}}`

func TestCallAndLogWithoutArguments(t *testing.T) {
	ts, logs := testServer(t, Options{})
	resp := post(t, ts.URL+"/mcp", initialize)
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"serverInfo"`) {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	// Cache-Control ставит и SDK («no-cache, no-transform»): ответ в любом случае не кэшируется.
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" || !strings.HasPrefix(resp.Header.Get("Cache-Control"), "no-") ||
		resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("заголовки: %v", resp.Header)
	}
	secret := "password=Hunter2-very-secret"
	call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"find_error","arguments":{"text":"FATAL: alpha pool is exhausted ` + secret + `"}}}`
	resp = post(t, ts.URL+"/mcp", call)
	b, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(b), "alpha-connections") {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	out := logs.String()
	if strings.Contains(out, "Hunter2") || strings.Contains(out, "alpha pool") {
		t.Errorf("в журнал попали аргументы:\n%s", out)
	}
	if !strings.Contains(out, `rpc="tools/call" tool="find_error"`) || !strings.Contains(out, `client="tester 9"`) {
		t.Errorf("в журнале нет метода, инструмента или клиента:\n%s", out)
	}
}

// The SDK client negotiates the newest protocol (2026-07-28, sessionless, server/discover)
// and falls back to initialize for older versions: both must work over HTTP.
func TestSDKClientOverHTTP(t *testing.T) {
	ts, logs := testServer(t, Options{})
	for _, proto := range []string{"", "2025-06-18"} {
		cl := mcp.NewClient(&mcp.Implementation{Name: "sdk", Version: "1"}, nil)
		cs, err := cl.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp"}, &mcp.ClientSessionOptions{ProtocolVersion: proto})
		if err != nil {
			t.Fatalf("%q: %v", proto, err)
		}
		tl, err := cs.ListTools(context.Background(), nil)
		if err != nil || len(tl.Tools) != 8 {
			t.Fatalf("%q: tools %v %v", proto, tl, err)
		}
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "component_versions", Arguments: map[string]any{"component": "rabbit"}})
		if err != nil || res.IsError {
			t.Fatalf("%q: %v %v", proto, res, err)
		}
		if got := cs.InitializeResult().ProtocolVersion; proto != "" && got != proto {
			t.Errorf("версия протокола %s, ждали %s", got, proto)
		}
		cs.Close()
	}
	if !strings.Contains(logs.String(), `rpc="server/discover"`) {
		t.Errorf("клиент SDK не спросил server/discover:\n%s", logs.String())
	}
}

func TestRateLimit(t *testing.T) {
	ts, _ := testServer(t, Options{Rate: rate.Limit(0.001), Burst: 2})
	codes := []int{}
	for i := 0; i < 4; i++ {
		resp := post(t, ts.URL+"/mcp", initialize)
		codes = append(codes, resp.StatusCode)
		resp.Body.Close()
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 || codes[3] != 429 {
		t.Errorf("коды: %v", codes)
	}
}

func TestBodyLimit(t *testing.T) {
	ts, _ := testServer(t, Options{MaxBody: 1024})
	big := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"find_error","arguments":{"text":"` + strings.Repeat("a", 5000) + `"}}}`
	resp := post(t, ts.URL+"/mcp", big)
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("код %d", resp.StatusCode)
	}
}

func TestBrowserAndPreflight(t *testing.T) {
	ts, _ := testServer(t, Options{})
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/mcp", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "https://drxinfra.ru/mcp.html" {
		t.Errorf("браузер: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/mcp", nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, _ = client.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET потока без сессий: %d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodOptions, ts.URL+"/mcp", nil)
	req.Header.Set("Origin", "https://example.org")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, _ = client.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || !strings.Contains(resp.Header.Get("Access-Control-Allow-Methods"), "POST") {
		t.Errorf("preflight: %d %v", resp.StatusCode, resp.Header)
	}
	resp, _ = client.Get(ts.URL + "/healthz")
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || strings.TrimSpace(string(b)) != "ok" {
		t.Errorf("healthz: %d %q", resp.StatusCode, b)
	}
	resp, _ = client.Get(ts.URL + "/etc/passwd")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("чужой путь: %d", resp.StatusCode)
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Header.Set("X-Real-IP", "203.0.113.7")
	if ip := clientIP(r); ip != "203.0.113.7" {
		t.Errorf("от прокси: %s", ip)
	}
	r.RemoteAddr = "198.51.100.1:5555"
	if ip := clientIP(r); ip != "198.51.100.1" {
		t.Errorf("заголовок не от прокси принят: %s", ip)
	}
}

func TestLimiterTableIsBounded(t *testing.T) {
	l := newLimiter(rate.Inf, 1)
	for i := 0; i < maxClients; i++ {
		l.allow(string(rune(i)))
	}
	if l.allow("ещё один") {
		t.Error("таблица адресов не ограничена")
	}
	if len(l.byIP) != maxClients {
		t.Errorf("записей %d", len(l.byIP))
	}
}
