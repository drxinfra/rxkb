package kb

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fixture is built from testdata/site, a tiny fake of the site sources.
func fixture(t *testing.T) *KB {
	t.Helper()
	k, err := LoadFile(filepath.Join("..", "..", "testdata", "kb.json"))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestBuilderReproducesFixture(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("нет python3")
	}
	out := filepath.Join(t.TempDir(), "kb.json")
	cmd := exec.Command(py, filepath.Join("..", "..", "hack", "build-kb.py"), "--site", filepath.Join("..", "..", "testdata", "site"), "--out", out)
	cmd.Env = append(os.Environ(), "SITE_DOMAIN=", "TG_USERNAME=", "CONTACT_EMAIL=")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build-kb.py: %v\n%s", err, b)
	}
	got, _ := os.ReadFile(out)
	want, _ := os.ReadFile(filepath.Join("..", "..", "testdata", "kb.json"))
	if !bytes.Equal(got, want) {
		t.Fatal("testdata/kb.json устарел: python3 hack/build-kb.py --site testdata/site --out testdata/kb.json")
	}
}

func TestLoad(t *testing.T) {
	k := fixture(t)
	if len(k.Errors) != 3 || len(k.Articles) != 2 || len(k.Search) != 7 {
		t.Fatalf("errors %d, articles %d, search %d", len(k.Errors), len(k.Articles), len(k.Search))
	}
	if k.Articles[0].ID != "second-post" {
		t.Errorf("статьи не по дате: %s первая", k.Articles[0].ID)
	}
	if k.ErrorByID("beta-broker") == nil || k.ArticleByID("first-post") == nil || k.ArticleByID("draft-post") != nil {
		t.Error("индексы по id")
	}
	if !strings.Contains(k.ArticleByID("first-post").Markdown, "](https://drxinfra.ru/errors/beta-broker.html)") {
		t.Error("относительные ссылки не стали полными")
	}
	if !strings.Contains(k.Search[0].X, "support@drxinfra.ru") || !strings.Contains(k.Search[1].X, "drxinfra.ru.") {
		t.Error("плейсхолдеры не подставлены")
	}
	if len(k.Warnings) == 0 {
		t.Error("тестовые скрипты отличаются от сайта: ждём предупреждение о порте")
	}
	re2, bt, dropped := k.PatternStats()
	if re2 != 4 || bt != 1 || dropped != 0 {
		t.Errorf("шаблоны: RE2 %d, regexp2 %d, отброшено %d", re2, bt, dropped)
	}
}

func TestLoadRejectsBroken(t *testing.T) {
	for name, raw := range map[string]string{
		"не JSON":       "{",
		"другой формат": `{"format": 2}`,
		"пустая база":   `{"format": 1, "site": "https://x", "help": "https://x/#c"}`,
	} {
		if _, err := Load([]byte(raw)); err == nil {
			t.Errorf("%s: загрузилось", name)
		}
	}
}

func ids(hits []ErrorHit) string {
	var s []string
	for _, h := range hits {
		s = append(s, h.Error.ID)
	}
	return strings.Join(s, ",")
}

func TestFindErrors(t *testing.T) {
	k := fixture(t)
	cases := map[string]string{
		"2026-01-01 ERROR FATAL: alpha pool is exhausted (ALPHA-123)": "alpha-connections",
		"BetaBrokerException: None of the endpoints were reachable":   "beta-broker",
		"gamma timeout after 30 s":                                    "gamma-lookbehind",
		"ALPHA-12":                                                    "",
		"совсем другое":                                               "",
		"abc":                                                         "", // короче 4 символов
	}
	for text, want := range cases {
		norm, _ := NormalizeLog(text)
		if got := ids(k.FindErrors(norm)); got != want {
			t.Errorf("%q: %q, ждали %q", text, got, want)
		}
	}
	// Длиннее совпадение — выше: обе ошибки найдены, альфа совпала длиннее.
	norm, _ := NormalizeLog("alpha pool is exhausted; BetaBrokerException")
	if got := ids(k.FindErrors(norm)); got != "alpha-connections,beta-broker" {
		t.Errorf("порядок: %s", got)
	}
	// Без совпадения шаблона — по словам заголовка и описания.
	norm, _ = NormalizeLog("Сервис ничего не знает, брокера нет: достучался?")
	hits := k.FindErrors(norm)
	if len(hits) != 1 || !hits[0].ByWords || hits[0].Error.ID != "beta-broker" {
		t.Errorf("поиск по словам: %+v", hits)
	}
	// Шаблон с просмотром назад (regexp2) не срабатывает после «retry»: находится только по словам.
	norm, _ = NormalizeLog("retry gamma timeout after 30 s")
	hits = k.FindErrors(norm)
	if len(hits) != 1 || !hits[0].ByWords || hits[0].Error.ID != "gamma-lookbehind" {
		t.Errorf("просмотр назад: %+v", hits)
	}
}

func TestNormalizeLog(t *testing.T) {
	s, cut := NormalizeLog("\r\n  line1\r\nline2\r" + string(rune(0xfeff)))
	if s != "line1\nline2" || cut {
		t.Errorf("%q %v", s, cut)
	}
	long := strings.Repeat("я", MaxLogChars+10)
	s, cut = NormalizeLog(long)
	if !cut || len([]rune(s)) != MaxLogChars {
		t.Errorf("обрезка: %d %v", len([]rune(s)), cut)
	}
}

func TestSearch(t *testing.T) {
	k := fixture(t)
	hits, strict, total := k.SearchSite("очередь растёт")
	if !strict || total != 1 || hits[0].Doc.U != "https://drxinfra.ru/blog/first-post.html" {
		t.Fatalf("strict %v total %d first %v", strict, total, hits)
	}
	if !strings.Contains(hits[0].Snippet, "Очередь растёт") {
		t.Errorf("сниппет: %q", hits[0].Snippet)
	}
	hits, strict, _ = k.SearchSite("очереди марсиане")
	if strict || len(hits) == 0 {
		t.Errorf("частичное совпадение: strict %v, %d", strict, len(hits))
	}
	if hits, _, _ := k.SearchSite("я"); hits != nil {
		t.Error("однобуквенный запрос")
	}
	if got := SearchTerms("Растущая очередь ё"); strings.Join(got, " ") != "растущ очере" {
		t.Errorf("термины: %v", got)
	}
}

func TestSizing(t *testing.T) {
	k := fixture(t)
	e := k.Sizing()
	if strings.Join(e.Versions, ",") != "2.0,1.0" || len(e.Keys) != 6 {
		t.Fatalf("versions %v keys %v", e.Versions, e.Keys)
	}
	r, err := e.Compute(map[string]any{"users": 500.0, "ha": 1.0})
	if err != nil {
		t.Fatal(err)
	}
	if r.Hash != "users=500&ha=1" || len(r.Res.Contours) != 2 || r.Res.Contours[0].Totals.VMs != 4 || !r.Res.Contours[0].HA {
		t.Fatalf("hash %q contours %+v", r.Hash, r.Res.Contours)
	}
	if !strings.HasPrefix(r.Text, "Тестовый сайзинг 2.0") {
		t.Errorf("текст: %q", r.Text)
	}
	start := time.Now()
	if _, err := e.Compute(map[string]any{"users": 99999.0}); err == nil {
		t.Fatal("бесконечный цикл не прерван")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("прерывание через %v", d)
	}
	if _, err := e.Compute(map[string]any{}); err != nil {
		t.Errorf("после прерывания: %v", err)
	}
}

func TestAIPlan(t *testing.T) {
	k := fixture(t)
	m := k.AI()
	if strings.Join(m.Models, ",") != "small,big" || m.Default != "small" {
		t.Fatalf("models %v default %s", m.Models, m.Default)
	}
	demand := 300.0 * 20 / 100 * 2
	p := m.Plan("small", "srv16", "all", "f16", 60, demand)
	if p.C != 1 || p.Time.P50 != 40 || p.Cap != 90 || !p.Fits || p.Servers != 2 || p.RAMRound != 8 {
		t.Errorf("small/srv16: %+v", p)
	}
	p = m.Plan("big", "srv16", "all", "f16", 60, demand)
	if p.C != 2 || p.Servers != 1 || p.RAMRound != 32 || p.Time.P50 < 41.99 || p.Time.P50 > 42.01 {
		t.Errorf("big/srv16: %+v", p)
	}
	p = m.Plan("small", "lap8", "all", "f16", 60, demand)
	if p.C != 1 || p.Time.P50 != 80 || p.Fits || p.Time.Measured || p.Servers != 3 {
		t.Errorf("small/lap8: %+v", p)
	}
	if m.Plan("big", "lap8", "all", "f16", 60, demand) != nil {
		t.Error("big/lap8: замеров нет, а план есть")
	}
	if q := m.Quality("big", "pilot"); q == nil || !q.Fallback || q.Passed != 42 {
		t.Errorf("качество с другим набором: %+v", q)
	}
	if c, ok := m.ColdTTFT("small", "srv16", "q8_0"); !ok || c != 120 {
		t.Errorf("холодный старт: %v %v", c, ok)
	}
}

func TestUpgradeLink(t *testing.T) {
	k := fixture(t)
	page := "https://drxinfra.ru/upgrade-checklist.html"
	cases := []struct {
		done           map[int]bool
		from, to, when string
		want           string
	}{
		{nil, "", "", "", page},
		{map[int]bool{1: true}, "", "", "", page + "?d=8"},
		{map[int]bool{1: true, 3: true, 5: true}, "", "", "", page + "?d=a8"},
		{map[int]bool{1: true, 2: true, 3: true, 4: true, 5: true}, "1.0", "2.0", "", page + "?d=f8&from=1.0&to=2.0"},
		{nil, `4.12 <b>"x"</b>`, "", "сб 22:00", page + "?from=4.12%20bx%2Fb&when=%D1%81%D0%B1%2022%3A00"},
	}
	for _, c := range cases {
		if got := k.UpgradeLink(c.done, c.from, c.to, c.when); got != c.want {
			t.Errorf("%v: %s, ждали %s", c.done, got, c.want)
		}
	}
	if got := EncodeURIComponent("a b!~*'()-_.é"); got != "a%20b!~*'()-_.%C3%A9" {
		t.Errorf("encodeURIComponent: %s", got)
	}
}

func TestScoreSecurity(t *testing.T) {
	k := fixture(t)
	r := k.ScoreSecurity(map[string]string{"q1": "y", "q2": "n"})
	if *r.Score != 50 || r.Zone != "red" || r.CritOpen != 1 || len(r.Gaps) != 1 || !strings.HasSuffix(r.Link, "?a=yn") {
		t.Errorf("%+v", r)
	}
	r = k.ScoreSecurity(map[string]string{"q1": "y", "q2": "y", "q3": "n"})
	if *r.Score != 95 || r.Zone != "green" || r.Gaps[0].Q.ID != "q3" {
		t.Errorf("%+v", r)
	}
	r = k.ScoreSecurity(map[string]string{"q3": "p"})
	if *r.Score != 50 || r.Zone != "yellow" || !strings.HasSuffix(r.Link, "?a=--p") {
		t.Errorf("%+v", r)
	}
	r = k.ScoreSecurity(nil)
	if r.Score != nil || r.Zone != "" || r.Link != "https://drxinfra.ru/security-check.html" {
		t.Errorf("%+v", r)
	}
	// Порядок дыр: вес по убыванию, при равном весе «нет» раньше «частично».
	r = k.ScoreSecurity(map[string]string{"q1": "p", "q2": "n", "q3": "u"})
	if r.Gaps[0].Q.ID != "q2" || r.Gaps[1].Q.ID != "q1" || r.Gaps[2].Q.ID != "q3" {
		t.Errorf("порядок: %v %v %v", r.Gaps[0].Q.ID, r.Gaps[1].Q.ID, r.Gaps[2].Q.ID)
	}
}
