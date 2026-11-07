// Package tools exposes the drxinfra.ru knowledge base as MCP tools.
//
// Every tool is read-only and works on the in-memory bundle only. Names and
// descriptions are in English for agents; the content is Russian, as on the
// site. Every answer ends with the drxinfra.ru page it came from and one line
// on where to ask for help.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/drxinfra/rxkb/internal/kb"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MaxAnswerChars caps the text of one answer.
const MaxAnswerChars = 40000

// callTimeout bounds one tool call; the work is in memory and takes milliseconds.
const callTimeout = 5 * time.Second

const instructions = `Knowledge base of drxinfra.ru for Directum RX operations: read-only, public, no authorization.

- find_error: paste a log line or an error text; it is matched against the reference of about 200 errors of RX, Directum Launcher, PostgreSQL, RabbitMQ, Elasticsearch, certificates, Keycloak, nginx, Docker and Kubernetes, with what it means, how to check and what to do.
- get_article: the full text of an error page or a blog post by id or drxinfra.ru URL.
- search_site: articles, error pages and tool pages of the site, ranked like the site search.
- size_rx and size_ai: the sizing calculators of the site (Directum RX servers; hardware for a local AI assistant). The numbers are the same as on the pages, and the result links to the same calculation on the site.
- component_versions, upgrade_checklist, security_self_check: version table, upgrade checklist and the weighted security questions.

The content is in Russian. Answer in the user's language, but keep commands, settings, file names and error strings exactly as given. Give the user the drxinfra.ru link from the result. Before sending a log line, remove passwords, tokens, host names and personal data from it: the server needs only the error text.`

// New builds an MCP server over a loaded knowledge base.
func New(k *kb.KB, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:        "rxkb",
		Title:       "drxinfra knowledge base for Directum RX",
		Description: "Errors, articles, sizing calculators and checklists of drxinfra.ru for Directum RX administrators",
		Version:     version,
		WebsiteURL:  k.Site + "/mcp.html",
		Icons:       []mcp.Icon{{Source: k.Site + "/favicon.svg", MIMEType: "image/svg+xml", Sizes: []string{"any"}}},
	}, &mcp.ServerOptions{
		Instructions: instructions,
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
		SetCacheable: func(_ context.Context, _ mcp.Request, c *mcp.Cacheable) {
			if c.TTLMs == 0 {
				c.TTLMs = 3600_000 // the tool list changes with a release, not between calls
			}
		},
	})
	t := &toolset{k: k}
	t.addFindError(s)
	t.addGetArticle(s)
	t.addSearch(s)
	t.addSizeRX(s)
	t.addSizeAI(s)
	t.addVersions(s)
	t.addUpgrade(s)
	t.addSecurity(s)
	return s
}

type toolset struct{ k *kb.KB }

var readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: ptr(false), OpenWorldHint: ptr(false)}

func ptr[T any](v T) *T { return &v }

// add registers a tool with a timeout around its handler.
func add[In any](s *mcp.Server, t *mcp.Tool, h func(context.Context, In) (string, any, error)) {
	if t.Annotations == nil {
		a := *readOnly
		a.Title = t.Title
		t.Annotations = &a
	}
	mcp.AddTool(s, t, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		ctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		text, out, err := h(ctx, in)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: capText(text, MaxAnswerChars)}}}, out, nil
	})
}

// footer ends every answer: the page and where to ask for help.
func (t *toolset) footer(url string) string {
	return "\n\nИсточник: " + url + "\nНужна помощь — " + t.k.Help
}

// capText cuts a text to n characters on a line break and says so.
func capText(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)[:n]
	cut := string(r)
	if i := strings.LastIndex(cut, "\n"); i > len(cut)/2 {
		cut = cut[:i]
	}
	return closeFences(cut) + "\n\n[ответ обрезан, полный текст по ссылке]"
}

// clip cuts one section of an article to n characters, keeping code blocks closed.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	cut := string([]rune(s)[:n])
	if i := strings.LastIndexAny(cut, "\n"); i > len(cut)/2 {
		cut = cut[:i]
	}
	return closeFences(strings.TrimRight(cut, " \n")) + " …"
}

func closeFences(s string) string {
	if strings.Count(s, "```")%2 == 1 {
		return s + "\n```"
	}
	return s
}

// schema helpers

func obj(props map[string]*jsonschema.Schema, order []string, required ...string) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", Properties: props, PropertyOrder: order, Required: required,
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}}}
}

func str(desc string, maxLen int) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Description: desc, MaxLength: ptr(maxLen)}
}

func enum(desc string, def any, values ...any) *jsonschema.Schema {
	s := &jsonschema.Schema{Description: desc, Enum: values, Default: raw(def)}
	switch values[0].(type) {
	case string:
		s.Type = "string"
	case float64, int:
		s.Type = "number"
	}
	return s
}

func num(desc string, def, min, max float64, integer bool) *jsonschema.Schema {
	s := &jsonschema.Schema{Type: "number", Description: desc, Minimum: ptr(min), Maximum: ptr(max), Default: raw(def)}
	if integer {
		s.Type = "integer"
	}
	return s
}

func boolean(desc string, def bool) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "boolean", Description: desc, Default: raw(def)}
}

func raw(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}

// formatting helpers for Russian text

// n0 is Math.round(v).toLocaleString('ru-RU') of the site, with an ordinary
// space between thousands (ru groups from five digits).
func n0(v float64) string {
	return group(fmt.Sprintf("%d", int64(jsRound(v))))
}

// n1 rounds to one decimal and prints it with a decimal comma.
func n1(v float64) string {
	x := jsRound(v*10) / 10
	s := fmt.Sprintf("%.1f", x)
	s = strings.TrimSuffix(s, ".0")
	ip, fp, _ := strings.Cut(s, ".")
	if fp != "" {
		return group(ip) + "," + fp
	}
	return group(ip)
}

func group(ip string) string {
	neg := strings.HasPrefix(ip, "-")
	ip = strings.TrimPrefix(ip, "-")
	if len(ip) >= 5 {
		var b strings.Builder
		for i, c := range ip {
			if i > 0 && (len(ip)-i)%3 == 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(c)
		}
		ip = b.String()
	}
	if neg {
		return "-" + ip
	}
	return ip
}

// jsRound is Math.round: halves go up.
func jsRound(v float64) float64 {
	f := float64(int64(v))
	if v < 0 && v != f {
		f--
	}
	if v-f >= 0.5 {
		return f + 1
	}
	return f
}

// sec prints seconds like the AI calculator: below 90 s in seconds, else in minutes.
func sec(v float64) string {
	if v < 90 {
		return n0(v) + " с"
	}
	return n1(v/60) + " мин"
}

func plural(n int, one, few, many string) string {
	m, h := n%10, n%100
	switch {
	case m == 1 && h != 11:
		return one
	case m >= 2 && m <= 4 && (h < 10 || h >= 20):
		return few
	default:
		return many
	}
}

// fmtGB is fmtGB of sizing.js: terabytes with a decimal comma from 1000 GB.
func fmtGB(gb float64) string {
	if gb >= 1000 {
		return strings.ReplaceAll(trimFloat(jsRound(gb/100)/10), ".", ",") + " ТБ"
	}
	return trimFloat(gb) + " ГБ"
}

func trimFloat(v float64) string {
	s := fmt.Sprintf("%.6f", v)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return s
}
