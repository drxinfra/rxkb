package tools

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/drxinfra/rxkb/internal/kb"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---------------------------------------------------------------- find_error

type findErrorIn struct {
	Text  string `json:"text"`
	Limit int    `json:"limit"`
}

type errorMatch struct {
	ID        string `json:"id"`
	Error     string `json:"error"`
	Summary   string `json:"summary"`
	Component string `json:"component"`
	Meaning   string `json:"meaning"`
	Check     string `json:"check"`
	Fix       string `json:"fix"`
	URL       string `json:"url"`
}

type findErrorOut struct {
	Found     int          `json:"found"`
	ByWords   bool         `json:"by_words"`
	InputCut  bool         `json:"input_cut"`
	Matches   []errorMatch `json:"matches"`
	Reference string       `json:"reference"`
	Help      string       `json:"help"`
}

const sectionChars = 2000

func (t *toolset) addFindError(s *mcp.Server) {
	add(s, &mcp.Tool{
		Name:  "find_error",
		Title: "Find a Directum RX error by log line",
		Description: "Match a log line, stack trace fragment or error message against the drxinfra.ru reference of about 200 errors " +
			"seen around Directum RX: RX services and Directum Launcher, PostgreSQL, RabbitMQ, Elasticsearch, certificates and TLS, " +
			"Keycloak and Kerberos, nginx, Docker and Kubernetes. Returns the best matches first, each with the exact error text, " +
			"what it means, how to check and what to do (Russian), and the page link. Uses the same patterns and ranking as the " +
			"search box on drxinfra.ru/errors/. Paste the error itself; remove passwords, tokens and personal data first.",
		InputSchema: obj(map[string]*jsonschema.Schema{
			"text":  str("Log line or error text, as is. The first 16000 characters are checked.", 20000),
			"limit": num("How many matches to return, best first", 3, 1, 6, true),
		}, []string{"text", "limit"}, "text"),
	}, t.findError)
}

func (t *toolset) findError(_ context.Context, in findErrorIn) (string, any, error) {
	text, cut := kb.NormalizeLog(in.Text)
	if len([]rune(text)) < 4 {
		return "", nil, fmt.Errorf("text is too short: paste the error line from the log")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 3
	}
	hits := t.k.FindErrors(text)
	ref := t.k.Site + "/errors/"
	out := findErrorOut{Found: len(hits), InputCut: cut, Reference: ref, Help: t.k.Help, Matches: []errorMatch{}}
	var b strings.Builder
	if len(hits) == 0 {
		b.WriteString("В справочнике ошибок drxinfra.ru такой ошибки пока нет. Пришлите строку нам: разберём и добавим в справочник.\n")
		b.WriteString("Можно поискать по словам инструментом search_site.")
		b.WriteString(t.footer(ref))
		return b.String(), out, nil
	}
	out.ByWords = hits[0].ByWords
	if out.ByWords {
		fmt.Fprintf(&b, "Точного совпадения со строками справочника нет, похожие по словам: %d.", len(hits))
	} else {
		fmt.Fprintf(&b, "Справочник ошибок drxinfra.ru: %d %s.", len(hits), plural(len(hits), "совпадение", "совпадения", "совпадений"))
	}
	if cut {
		fmt.Fprintf(&b, " Проверены первые %d символов текста.", kb.MaxLogChars)
	}
	if len(hits) > limit {
		fmt.Fprintf(&b, " Показаны первые %d.", limit)
		hits = hits[:limit]
	}
	for i, h := range hits {
		e := h.Error
		m := errorMatch{ID: e.ID, Error: e.Title, Summary: e.What, Component: e.Component,
			Meaning: clip(e.Sections["meaning"], sectionChars), Check: clip(e.Sections["check"], sectionChars),
			Fix: clip(e.Sections["fix"], sectionChars), URL: e.URL}
		out.Matches = append(out.Matches, m)
		fmt.Fprintf(&b, "\n\n## %d. %s\n%s · `%s`\n%s (id для get_article: errors/%s)", i+1, e.What, e.Component, oneLine(e.Title), e.URL, e.ID)
		for _, sec := range [][2]string{{"Что это значит", m.Meaning}, {"Как проверить", m.Check}, {"Что сделать", m.Fix}} {
			if sec[1] != "" {
				fmt.Fprintf(&b, "\n\n### %s\n%s", sec[0], sec[1])
			}
		}
	}
	b.WriteString(t.footer(ref))
	return b.String(), out, nil
}

func oneLine(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "`", "'"), "\n", " ")
}

// ---------------------------------------------------------------- get_article

type getArticleIn struct {
	ID string `json:"id"`
}

type articleOut struct {
	Kind        string   `json:"kind"`
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Error       string   `json:"error,omitempty"`
	Description string   `json:"description"`
	Component   string   `json:"component,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Date        string   `json:"date"`
	Updated     string   `json:"updated,omitempty"`
	URL         string   `json:"url"`
	Truncated   bool     `json:"truncated"`
	Help        string   `json:"help"`
}

const articleChars = 30000

func (t *toolset) addGetArticle(s *mcp.Server) {
	add(s, &mcp.Tool{
		Name:  "get_article",
		Title: "Get a drxinfra.ru article",
		Description: "Full text (Markdown, Russian) of a drxinfra.ru error page or blog article about Directum RX operations. " +
			"Accepts an id as returned by find_error and search_site (errors/<slug> or blog/<slug>), a bare slug, " +
			"or a drxinfra.ru URL such as https://drxinfra.ru/blog/rabbitmq-queue-growth.html. Links inside are absolute.",
		InputSchema: obj(map[string]*jsonschema.Schema{
			"id": str("errors/<slug>, blog/<slug>, a slug or a drxinfra.ru URL", 300),
		}, []string{"id"}, "id"),
	}, t.getArticle)
}

// resolveID turns an id or URL into ("errors"|"blog", slug).
func resolveID(id string) (string, string) {
	id = strings.TrimSpace(id)
	if u, err := url.Parse(id); err == nil && u.Host != "" {
		id = u.Path
	} else if strings.HasPrefix(id, "drxinfra.ru/") || strings.HasPrefix(id, "www.drxinfra.ru/") {
		_, id, _ = strings.Cut(id, "/")
	}
	if i := strings.IndexAny(id, "?#"); i >= 0 {
		id = id[:i]
	}
	id = strings.Trim(id, "/")
	id = strings.TrimSuffix(id, ".html")
	id = strings.TrimSuffix(id, ".md")
	parts := strings.Split(id, "/")
	switch {
	case len(parts) == 2 && (parts[0] == "errors" || parts[0] == "blog"):
		return parts[0], parts[1]
	case len(parts) == 3 && parts[1] == "posts" && (parts[0] == "errors" || parts[0] == "blog"):
		return parts[0], parts[2]
	case len(parts) == 1:
		return "", parts[0]
	}
	return "?", id
}

func (t *toolset) getArticle(_ context.Context, in getArticleIn) (string, any, error) {
	kind, slug := resolveID(in.ID)
	var e *kb.Error
	var a *kb.Article
	switch kind {
	case "errors":
		e = t.k.ErrorByID(slug)
	case "blog":
		a = t.k.ArticleByID(slug)
	case "":
		e, a = t.k.ErrorByID(slug), t.k.ArticleByID(slug)
		if e != nil && a != nil {
			return "", nil, fmt.Errorf("%q is both an error page and an article: use errors/%s or blog/%s", slug, slug, slug)
		}
	}
	var b strings.Builder
	var out articleOut
	switch {
	case e != nil:
		out = articleOut{Kind: "error", ID: "errors/" + e.ID, Title: e.What, Error: e.Title, Description: e.Description,
			Component: e.Component, Date: e.Date, URL: e.URL}
		fmt.Fprintf(&b, "# %s\n\nОшибка: `%s`\nКомпонент: %s · справочник ошибок drxinfra.ru, %s\n%s\n\n%s\n\n%s",
			e.What, oneLine(e.Title), e.Component, e.Date, e.URL, e.Description, e.Markdown)
	case a != nil:
		out = articleOut{Kind: "article", ID: "blog/" + a.ID, Title: a.Title, Description: a.Description, Tags: a.Tags,
			Date: a.Date, Updated: a.Updated, URL: a.URL}
		meta := "Блог drxinfra.ru, " + a.Date
		if a.Updated != "" && a.Updated != a.Date {
			meta += ", обновлено " + a.Updated
		}
		if len(a.Tags) > 0 {
			meta += " · " + strings.Join(a.Tags, ", ")
		}
		fmt.Fprintf(&b, "# %s\n\n%s\n%s\n\n%s\n\n%s", a.Title, meta, a.URL, a.Description, a.Markdown)
	default:
		return "", nil, fmt.Errorf("no article %q on drxinfra.ru: find it with search_site or find_error", in.ID)
	}
	text := b.String()
	if len([]rune(text)) > articleChars {
		text = capText(text, articleChars)
		out.Truncated = true
	}
	out.Help = t.k.Help
	return text + t.footer(out.URL), out, nil
}
