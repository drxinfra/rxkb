package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/drxinfra/rxkb/internal/kb"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func session(t *testing.T) *mcp.ClientSession {
	t.Helper()
	k, err := kb.LoadFile(filepath.Join("..", "..", "testdata", "kb.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := New(k, "test").Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, map[string]any, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	var sc map[string]any
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(b, &sc)
	}
	return text, sc, res.IsError
}

func TestToolList(t *testing.T) {
	cs := session(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s: не помечен как только чтение", tool.Name)
		}
		if tool.Description == "" || tool.Title == "" {
			t.Errorf("%s: нет описания или заголовка", tool.Name)
		}
	}
	sort.Strings(names)
	want := "component_versions find_error get_article search_site security_self_check size_ai size_rx upgrade_checklist"
	if strings.Join(names, " ") != want {
		t.Errorf("инструменты: %v", names)
	}
}

// Every answer links to drxinfra.ru and says where to ask for help.
func TestEveryAnswerHasLinkAndHelp(t *testing.T) {
	cs := session(t)
	calls := map[string]map[string]any{
		"find_error":          {"text": "FATAL: alpha pool is exhausted"},
		"get_article":         {"id": "blog/first-post"},
		"search_site":         {"query": "очередь"},
		"size_rx":             {"users": 1000},
		"size_ai":             {},
		"component_versions":  {},
		"upgrade_checklist":   {},
		"security_self_check": {},
	}
	for name, args := range calls {
		text, sc, isErr := call(t, cs, name, args)
		if isErr {
			t.Errorf("%s: ошибка: %s", name, text)
			continue
		}
		if !strings.Contains(text, "\nИсточник: https://drxinfra.ru/") || !strings.HasSuffix(text, "Нужна помощь — https://drxinfra.ru/#contact") {
			t.Errorf("%s: нет ссылки или строки помощи:\n%s", name, text)
		}
		if sc["help"] != "https://drxinfra.ru/#contact" {
			t.Errorf("%s: structured help = %v", name, sc["help"])
		}
	}
}

func TestFindError(t *testing.T) {
	cs := session(t)
	text, sc, _ := call(t, cs, "find_error", map[string]any{"text": "ERROR FATAL: alpha pool is exhausted (ALPHA-123)", "limit": 1})
	if !strings.Contains(text, "## 1. Кончились соединения в тестовом пуле") || !strings.Contains(text, "### Как проверить\n```sql") {
		t.Errorf("текст:\n%s", text)
	}
	m := sc["matches"].([]any)[0].(map[string]any)
	if m["id"] != "alpha-connections" || m["url"] != "https://drxinfra.ru/errors/alpha-connections.html" || m["fix"] == "" {
		t.Errorf("structured: %v", m)
	}
	text, sc, _ = call(t, cs, "find_error", map[string]any{"text": "ничего похожего здесь нет"})
	if sc["found"].(float64) != 0 || !strings.Contains(text, "пока нет") {
		t.Errorf("не найдено: %s", text)
	}
	if _, _, isErr := call(t, cs, "find_error", map[string]any{"text": "  ab "}); !isErr {
		t.Error("короткий текст должен быть ошибкой")
	}
	if _, _, isErr := call(t, cs, "find_error", map[string]any{"text": "x", "path": "/etc/passwd"}); !isErr {
		t.Error("лишний параметр должен быть ошибкой")
	}
}

func TestGetArticle(t *testing.T) {
	cs := session(t)
	for _, id := range []string{"blog/first-post", "first-post", "https://drxinfra.ru/blog/first-post.html#что-сделать", "drxinfra.ru/blog/first-post", "/blog/posts/first-post.md"} {
		text, sc, isErr := call(t, cs, "get_article", map[string]any{"id": id})
		if isErr || sc["id"] != "blog/first-post" || !strings.HasPrefix(text, "# Тестовая статья про очереди") {
			t.Errorf("%q: %v %v\n%s", id, isErr, sc, text)
		}
	}
	text, sc, _ := call(t, cs, "get_article", map[string]any{"id": "errors/beta-broker"})
	if sc["kind"] != "error" || !strings.Contains(text, "`BetaBrokerException: endpoints were not reachable`") {
		t.Errorf("ошибка: %s", text)
	}
	for _, id := range []string{"nope", "../../etc/passwd", "blog/../../etc/passwd", "https://evil.example/blog/first-post"} {
		if _, _, isErr := call(t, cs, "get_article", map[string]any{"id": id}); !isErr && id != "https://evil.example/blog/first-post" {
			t.Errorf("%q: нашлось", id)
		}
	}
}

func TestResolveID(t *testing.T) {
	cases := map[string][2]string{
		"errors/x":                            {"errors", "x"},
		"blog/x.html":                         {"blog", "x"},
		"x":                                   {"", "x"},
		"https://www.drxinfra.ru/errors/x":    {"errors", "x"},
		"https://drxinfra.ru/blog/x.html?a#b": {"blog", "x"},
		"../../etc/passwd":                    {"?", "../../etc/passwd"},
	}
	for in, want := range cases {
		if k, s := resolveID(in); k != want[0] || s != want[1] {
			t.Errorf("%q: %q %q", in, k, s)
		}
	}
}

func TestSearchSite(t *testing.T) {
	cs := session(t)
	text, sc, _ := call(t, cs, "search_site", map[string]any{"query": "очередь растёт", "limit": 5})
	r := sc["results"].([]any)[0].(map[string]any)
	if r["id"] != "blog/first-post" || sc["url"] != "https://drxinfra.ru/search.html?q=%D0%BE%D1%87%D0%B5%D1%80%D0%B5%D0%B4%D1%8C%20%D1%80%D0%B0%D1%81%D1%82%D1%91%D1%82" {
		t.Errorf("%v\n%s", sc, text)
	}
	_, sc, _ = call(t, cs, "search_site", map[string]any{"query": "очереди", "kind": "error"})
	for _, x := range sc["results"].([]any) {
		if x.(map[string]any)["kind"] != "Ошибка" {
			t.Errorf("фильтр по виду: %v", x)
		}
	}
	if _, _, isErr := call(t, cs, "search_site", map[string]any{"query": "!"}); !isErr {
		t.Error("пустой запрос должен быть ошибкой")
	}
}

func TestSizeRX(t *testing.T) {
	cs := session(t)
	text, sc, isErr := call(t, cs, "size_rx", map[string]any{"users": 500, "ha": true, "years": 3})
	if isErr {
		t.Fatal(text)
	}
	if sc["url"] != "https://drxinfra.ru/sizing.html#users=500&years=3&ha=1" {
		t.Errorf("ссылка: %v", sc["url"])
	}
	if strings.Contains(text, "Расчёт: drxinfra.ru/sizing") || !strings.HasPrefix(text, "Тестовый сайзинг 2.0") {
		t.Errorf("текст:\n%s", text)
	}
	in := sc["input"].(map[string]any)
	if in["ha"] != true || in["test"] != true || in["users"].(float64) != 500 {
		t.Errorf("вход: %v", in)
	}
	for _, bad := range []map[string]any{{"years": 10}, {"users": 1}, {"os": "macos"}, {"usersTotal": 5}, {"ver": "9.9"}} {
		if _, _, isErr := call(t, cs, "size_rx", bad); !isErr {
			t.Errorf("%v: должно быть ошибкой", bad)
		}
	}
}

func TestSizeAI(t *testing.T) {
	cs := session(t)
	text, sc, _ := call(t, cs, "size_ai", map[string]any{"model": "big"})
	p := sc["plan"].(map[string]any)
	if p["servers"].(float64) != 1 || p["ram_per_server_gb"].(float64) != 32 || p["concurrent_answers"].(float64) != 2 {
		t.Errorf("план: %v", p)
	}
	if !strings.Contains(text, "| **Big 30B** |") || !strings.Contains(text, "Итог:") {
		t.Errorf("текст:\n%s", text)
	}
	text, sc, _ = call(t, cs, "size_ai", map[string]any{"model": "big", "hw": "lap8"})
	if sc["plan"] != nil || !strings.Contains(text, "замеров нет") {
		t.Errorf("без замеров: %v\n%s", sc["plan"], text)
	}
	if _, _, isErr := call(t, cs, "size_ai", map[string]any{"model": "nodata"}); !isErr {
		t.Error("модель без замеров не должна приниматься")
	}
}

func TestVersions(t *testing.T) {
	cs := session(t)
	_, sc, _ := call(t, cs, "component_versions", map[string]any{"component": "postgres", "rx_version": "2.0.1"})
	rows := sc["rows"].([]any)
	if len(rows) != 1 || len(sc["columns"].([]any)) != 1 || sc["columns"].([]any)[0] != "RX 2.0" {
		t.Errorf("%v", sc)
	}
	text, sc, _ := call(t, cs, "component_versions", map[string]any{"rx_version": "1.1"})
	if sc["no_data"] == nil || len(sc["columns"].([]any)) != 2 || !strings.Contains(text, "данных в таблице нет") {
		t.Errorf("нет версии: %v", sc)
	}
	_, sc, _ = call(t, cs, "component_versions", map[string]any{"component": "astra"})
	if rows := sc["rows"].([]any); len(rows) != 1 || rows[0].(map[string]any)["component"] != "Linux" {
		t.Errorf("поиск по значению: %v", rows)
	}
}

func TestUpgradeChecklist(t *testing.T) {
	cs := session(t)
	text, sc, _ := call(t, cs, "upgrade_checklist", map[string]any{"done": []int{1, 3, 5}, "from_version": "1.0", "phase": 1})
	if sc["url"] != "https://drxinfra.ru/upgrade-checklist.html?d=a8&from=1.0" || sc["percent"].(float64) != 60 {
		t.Errorf("%v", sc)
	}
	if !strings.Contains(text, "[x] 1. Прочитать изменения — Требования к окружению") || strings.Contains(text, "Закрыть вход") ||
		!strings.Contains(text, "Проверить интеграции (если есть)") {
		t.Errorf("текст:\n%s", text)
	}
	if _, _, isErr := call(t, cs, "upgrade_checklist", map[string]any{"done": []int{99}}); !isErr {
		t.Error("номер вне чек-листа")
	}
}

func TestSecurity(t *testing.T) {
	cs := session(t)
	text, sc, _ := call(t, cs, "security_self_check", nil)
	if len(sc["questions"].([]any)) != 3 || !strings.Contains(text, "## Критично (вес 10)") {
		t.Errorf("вопросы:\n%s", text)
	}
	text, sc, _ = call(t, cs, "security_self_check", map[string]any{"answers": map[string]any{"q1": "y", "q2": "n"}})
	if sc["score"].(float64) != 50 || sc["zone"] != "red" || sc["url"] != "https://drxinfra.ru/security-check.html?a=yn" {
		t.Errorf("%v", sc)
	}
	if !strings.Contains(text, "Итог: 50 из 100, красная зона") || !strings.Contains(text, "- [Нет] Восстановление проверяли") {
		t.Errorf("текст:\n%s", text)
	}
	for _, bad := range []map[string]any{{"q1": "yes"}, {"q9": "y"}} {
		if _, _, isErr := call(t, cs, "security_self_check", map[string]any{"answers": bad}); !isErr {
			t.Errorf("%v: должно быть ошибкой", bad)
		}
	}
}

func TestFormat(t *testing.T) {
	cases := map[string]string{n0(1234): "1234", n0(12345.6): "12 346", n1(5.25): "5,3", n1(5.0): "5", n1(12345.67): "12 345,7",
		sec(42.4): "42 с", sec(89.6): "90 с", sec(150): "2,5 мин", fmtGB(2700): "2,7 ТБ", fmtGB(480): "480 ГБ"}
	for got, want := range cases {
		if got != want {
			t.Errorf("%q, ждали %q", got, want)
		}
	}
	if jsRound(-2.5) != -2 || jsRound(2.5) != 3 || jsRound(0.49999999999999994) != 0 {
		t.Error("jsRound")
	}
	if got := capText(strings.Repeat("строка\n", 100), 50); !strings.HasSuffix(got, "[ответ обрезан, полный текст по ссылке]") {
		t.Errorf("capText: %q", got)
	}
	if got := clip("```sh\n"+strings.Repeat("echo 1\n", 50), 40); !strings.Contains(got, "\n```") {
		t.Errorf("clip не закрыл блок кода: %q", got)
	}
}
