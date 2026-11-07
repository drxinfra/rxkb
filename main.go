// rxkb: public MCP server with the drxinfra.ru knowledge base for Directum RX
// administrators. Read-only, no authorization, no outbound connections: it
// serves one kb.json built from the site sources (hack/build-kb.py).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/drxinfra/rxkb/internal/kb"
	"github.com/drxinfra/rxkb/internal/tools"
	"github.com/drxinfra/rxkb/internal/web"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"
)

var version = "dev"

type state struct {
	kb     *kb.KB
	server *mcp.Server
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envFloat(name string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(name), 64); err == nil && v > 0 {
		return v
	}
	return def
}

func main() {
	listen := flag.String("listen", env("RXKB_LISTEN", "127.0.0.1:8790"), "address to listen on (RXKB_LISTEN)")
	file := flag.String("kb", env("RXKB_FILE", "/var/lib/rxkb/kb.json"), "knowledge base built by hack/build-kb.py (RXKB_FILE)")
	every := flag.Duration("reload", 30*time.Second, "how often to look for a new kb.json")
	check := flag.Bool("check", false, "load the knowledge base, print what is in it and exit")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	logger := log.New(os.Stdout, "", 0) // journald adds the time
	if *showVersion {
		fmt.Println("rxkb", version)
		return
	}
	k, err := kb.LoadFile(*file)
	if err != nil {
		logger.Fatalf("rxkb %s: %v", version, err)
	}
	for _, w := range k.Warnings {
		logger.Printf("warning: %s", w)
	}
	if *check {
		re2, bt, dropped := k.PatternStats()
		fmt.Printf("%s\nшаблоны ошибок: RE2 %d, regexp2 %d, не скомпилировались %d\n", k.Stats(), re2, bt, dropped)
		return
	}

	var cur atomic.Pointer[state]
	cur.Store(&state{kb: k, server: tools.New(k, version)})
	h := web.Handler(
		func() *mcp.Server { return cur.Load().server },
		func() string { return "ok rxkb " + version + " " + cur.Load().kb.Stats() },
		web.Options{
			Rate:          rate.Limit(envFloat("RXKB_RATE", 2)),
			Burst:         int(envFloat("RXKB_BURST", 30)),
			MaxConcurrent: int(envFloat("RXKB_MAX_CONCURRENT", 16)),
			MaxBody:       int64(envFloat("RXKB_MAX_BODY", 128<<10)),
			Page:          k.Site + "/mcp.html",
			Log:           logger,
		})
	srv := &http.Server{
		Addr:              *listen,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          log.New(discard{}, "", 0), // TLS and parse noise of scanners
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		logger.Fatalf("rxkb: %v", err)
	}
	logger.Printf("rxkb %s on %s, %s", version, ln.Addr(), k.Stats())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go watch(ctx, *file, *every, hup, &cur, logger)

	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatalf("rxkb: %v", err)
	}
}

// watch reloads kb.json when it changes (the site deploy replaces it) or on SIGHUP.
// A broken file is reported and skipped: the server keeps answering from the old one.
func watch(ctx context.Context, file string, every time.Duration, hup <-chan os.Signal, cur *atomic.Pointer[state], logger *log.Logger) {
	stamp := func() string {
		fi, err := os.Stat(file)
		if err != nil {
			return ""
		}
		return fi.ModTime().String() + "/" + strconv.FormatInt(fi.Size(), 10)
	}
	last := stamp()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		force := false
		select {
		case <-ctx.Done():
			return
		case <-hup:
			force = true
		case <-t.C:
		}
		now := stamp()
		if now == "" || (now == last && !force) {
			continue
		}
		last = now
		k, err := kb.LoadFile(file)
		if err != nil {
			logger.Printf("reload: %v (keeping %s)", err, cur.Load().kb.Hash)
			continue
		}
		if k.Hash == cur.Load().kb.Hash {
			continue
		}
		for _, w := range k.Warnings {
			logger.Printf("warning: %s", w)
		}
		cur.Store(&state{kb: k, server: tools.New(k, version)})
		logger.Printf("reloaded: %s", k.Stats())
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
