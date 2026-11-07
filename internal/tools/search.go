package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/drxinfra/rxkb/internal/kb"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type searchIn struct {
	Query string `json:"query"`
	Kind  string `json:"kind"`
	Limit int    `json:"limit"`
}

type searchResult struct {
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	ID      string `json:"id,omitempty"`
	Snippet string `json:"snippet"`
}

type searchOut struct {
	Query   string         `json:"query"`
	Found   int            `json:"found"`
	Exact   bool           `json:"exact"`
	Results []searchResult `json:"results"`
	URL     string         `json:"url"`
	Help    string         `json:"help"`
}

var kindLabel = map[string]string{"article": "Статья", "error": "Ошибка", "tool": "Инструмент", "page": "Страница"}

func (t *toolset) addSearch(s *mcp.Server) {
	add(s, &mcp.Tool{
		Name:  "search_site",
		Title: "Search drxinfra.ru",
		Description: "Search drxinfra.ru: blog articles on Directum RX infrastructure (PostgreSQL, RabbitMQ, Elasticsearch, backups, " +
			"monitoring, logs, Kubernetes, security, updates, AI assistants), the error reference and the tool pages (calculators, " +
			"config generators, checklists). Ranked exactly like the site search: Russian words match by stem and prefix, " +
			"titles weigh most. Query in Russian works best (e.g. «очередь растёт», «pg_wal», «сертификат»). " +
			"Returns titles, links, snippets and ids for get_article.",
		InputSchema: obj(map[string]*jsonschema.Schema{
			"query": str("Words to search for", 300),
			"kind":  enum("Only one kind of page: article, error, tool (calculators and generators) or page", "any", "any", "article", "error", "tool", "page"),
			"limit": num("How many results to return", 8, 1, 20, true),
		}, []string{"query", "kind", "limit"}, "query"),
	}, t.search)
}

// idFor returns the get_article id of a site URL, or "".
func (t *toolset) idFor(u string) string {
	p := strings.TrimPrefix(u, t.k.Site+"/")
	for _, kind := range []string{"blog/", "errors/"} {
		if strings.HasPrefix(p, kind) && strings.HasSuffix(p, ".html") {
			return strings.TrimSuffix(p, ".html")
		}
	}
	return ""
}

func (t *toolset) search(_ context.Context, in searchIn) (string, any, error) {
	q := strings.TrimSpace(in.Query)
	if len(kb.SearchTerms(q)) == 0 {
		return "", nil, fmt.Errorf("the query has no words to search for")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 8
	}
	hits, strict, total := t.k.SearchSite(q)
	page := t.k.Site + "/search.html?q=" + kb.EncodeURIComponent(q)
	out := searchOut{Query: q, Exact: strict, URL: page, Help: t.k.Help, Results: []searchResult{}}
	want := kindLabel[in.Kind]
	for _, h := range hits {
		if want != "" && h.Doc.K != want {
			continue
		}
		out.Results = append(out.Results, searchResult{Kind: h.Doc.K, Title: h.Doc.T, URL: h.Doc.U, ID: t.idFor(h.Doc.U), Snippet: h.Snippet})
	}
	out.Found = len(out.Results)
	if want == "" {
		out.Found = total
	}
	if len(out.Results) > limit {
		out.Results = out.Results[:limit]
	}
	var b strings.Builder
	switch {
	case len(out.Results) == 0:
		fmt.Fprintf(&b, "На drxinfra.ru по запросу «%s» ничего не нашлось. Попробуйте другое слово, а текст ошибки целиком — в find_error.", q)
	case strict:
		fmt.Fprintf(&b, "Поиск по drxinfra.ru, «%s»: найдено %d, показано %d.", q, out.Found, len(out.Results))
	default:
		fmt.Fprintf(&b, "Поиск по drxinfra.ru, «%s»: точных совпадений нет, похожее: %d, показано %d.", q, out.Found, len(out.Results))
	}
	for i, r := range out.Results {
		fmt.Fprintf(&b, "\n\n%d. [%s] %s\n%s", i+1, r.Kind, r.Title, r.URL)
		if r.ID != "" {
			fmt.Fprintf(&b, " (id: %s)", r.ID)
		}
		if r.Snippet != "" {
			b.WriteString("\n" + strings.TrimSpace(r.Snippet))
		}
	}
	b.WriteString(t.footer(page))
	return b.String(), out, nil
}
