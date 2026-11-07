package tools

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/drxinfra/rxkb/internal/kb"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---------------------------------------------------------------- component_versions

var releaseRe = regexp.MustCompile(`\d+\.\d+`)

type versionsIn struct {
	Component string `json:"component"`
	RXVersion string `json:"rx_version"`
}

type versionsOut struct {
	Columns []string        `json:"columns"`
	Rows    []kb.VersionRow `json:"rows"`
	Source  string          `json:"source"`
	Notes   []string        `json:"notes"`
	NoData  string          `json:"no_data,omitempty"`
	URL     string          `json:"url"`
	Help    string          `json:"help"`
}

func (t *toolset) addVersions(s *mcp.Server) {
	v := t.k.Versions
	add(s, &mcp.Tool{
		Name:  "component_versions",
		Title: "Directum RX component versions",
		Description: "Supported versions of Linux and Windows Server, Docker, PostgreSQL and compatible DBMS (Postgres Pro, " +
			"Jatoba, Tantor), Microsoft SQL Server, RabbitMQ, Elasticsearch and Kubernetes tools for Directum RX, as summarized " +
			"on drxinfra.ru from the vendor documents. The table covers " + strings.Join(v.Columns, " and ") + " only; for other " +
			"releases it says so instead of guessing. Optional filters by component and RX version.",
		InputSchema: obj(map[string]*jsonschema.Schema{
			"component":  str("Part of a component name or version text, e.g. PostgreSQL, Astra, RabbitMQ, Elasticsearch", 100),
			"rx_version": str("RX release, e.g. 4.12 or 25.2", 20),
		}, []string{"component", "rx_version"}),
	}, t.versions)
}

func (t *toolset) versions(_ context.Context, in versionsIn) (string, any, error) {
	v := t.k.Versions
	out := versionsOut{Source: v.Source, Notes: v.Notes, URL: v.Page, Help: t.k.Help}
	cols := []int{}
	var b strings.Builder
	b.WriteString("Версии компонентов Directum RX (сводка drxinfra.ru по документам вендора)\n" + v.Source)
	if rv := strings.TrimSpace(in.RXVersion); rv != "" {
		// A release is the first two numbers: 25.2 with any build number is 25.2, and 4.1 is not 4.12.
		want := releaseRe.FindString(rv)
		for i, c := range v.Columns {
			if want != "" && releaseRe.FindString(c) == want {
				cols = append(cols, i)
			}
		}
		if len(cols) == 0 {
			out.NoData = fmt.Sprintf("Для Directum RX %s данных в таблице нет: она сведена только для %s.", rv, strings.Join(v.Columns, " и "))
			fmt.Fprintf(&b, "\n\n%s Сверяйтесь с документом вендора «Типовые требования к аппаратному и программному обеспечению» своей версии. Ниже то, что есть для %s.", out.NoData, strings.Join(v.Columns, " и "))
		}
	}
	if len(cols) == 0 {
		for i := range v.Columns {
			cols = append(cols, i)
		}
	}
	for _, i := range cols {
		out.Columns = append(out.Columns, v.Columns[i])
	}
	f := strings.ToLower(strings.TrimSpace(in.Component))
	match := func(r kb.VersionRow) bool {
		if f == "" || strings.Contains(strings.ToLower(r.Component), f) {
			return true
		}
		for _, x := range r.Values {
			if strings.Contains(strings.ToLower(x), f) {
				return true
			}
		}
		return false
	}
	for _, r := range v.Rows {
		if match(r) {
			vals := make([]string, 0, len(cols))
			for _, i := range cols {
				vals = append(vals, r.Values[i])
			}
			out.Rows = append(out.Rows, kb.VersionRow{Component: r.Component, Values: vals})
		}
	}
	if len(out.Rows) == 0 {
		fmt.Fprintf(&b, "\n\nКомпонента «%s» в таблице нет, ниже она целиком.", in.Component)
		for _, r := range v.Rows {
			vals := make([]string, 0, len(cols))
			for _, i := range cols {
				vals = append(vals, r.Values[i])
			}
			out.Rows = append(out.Rows, kb.VersionRow{Component: r.Component, Values: vals})
		}
	}
	b.WriteString("\n\n| Компонент | " + strings.Join(out.Columns, " | ") + " |\n|---|" + strings.Repeat("---|", len(out.Columns)))
	for _, r := range out.Rows {
		b.WriteString("\n| " + r.Component + " | " + strings.Join(r.Values, " | ") + " |")
	}
	for _, n := range v.Notes {
		b.WriteString("\n\n" + n)
	}
	b.WriteString(t.footer(v.Page))
	return b.String(), out, nil
}

// ---------------------------------------------------------------- upgrade_checklist

type upgradeIn struct {
	Phase  int    `json:"phase"`
	Done   []int  `json:"done"`
	From   string `json:"from_version"`
	To     string `json:"to_version"`
	Window string `json:"window"`
}

type upgradeItemOut struct {
	N        int    `json:"n"`
	Text     string `json:"text"`
	Note     string `json:"note,omitempty"`
	Optional bool   `json:"optional,omitempty"`
	Done     bool   `json:"done"`
}

type upgradePhaseOut struct {
	N     int              `json:"n"`
	Title string           `json:"title"`
	Intro string           `json:"intro"`
	Items []upgradeItemOut `json:"items"`
}

type upgradeOut struct {
	Phases  []upgradePhaseOut `json:"phases"`
	Total   int               `json:"total"`
	Done    int               `json:"done"`
	Percent int               `json:"percent"`
	URL     string            `json:"url"`
	Help    string            `json:"help"`
}

func (t *toolset) addUpgrade(s *mcp.Server) {
	u := t.k.Upgrade
	add(s, &mcp.Tool{
		Name:  "upgrade_checklist",
		Title: "Directum RX upgrade checklist",
		Description: fmt.Sprintf("The drxinfra.ru checklist for upgrading Directum RX with a way back: %d items in %d phases, from two "+
			"weeks before the window through the rehearsal on a test contour, the rollback plan, the window itself, checks before "+
			"users come back and the first day. Optionally marks done items and returns a link that opens the same marks on "+
			"drxinfra.ru/upgrade-checklist.html, where they can be printed or saved.", u.Count, len(u.Phases)),
		InputSchema: obj(map[string]*jsonschema.Schema{
			"phase":        num("Only this phase (1-"+strconv.Itoa(len(u.Phases))+"); 0 = all", 0, 0, float64(len(u.Phases)), true),
			"done":         {Type: "array", Description: "Numbers of the items already done (1-" + strconv.Itoa(u.Count) + ")", Items: &jsonschema.Schema{Type: "integer", Minimum: ptr(1.0), Maximum: ptr(float64(u.Count))}, MaxItems: ptr(u.Count)},
			"from_version": str("Current RX version, for the link (up to 40 characters)", 100),
			"to_version":   str("Target RX version, for the link (up to 40 characters)", 100),
			"window":       str("Planned window, for the link, e.g. 15.12 22:00-06:00 (up to 40 characters)", 100),
		}, []string{"phase", "done", "from_version", "to_version", "window"}),
	}, t.upgrade)
}

func (t *toolset) upgrade(_ context.Context, in upgradeIn) (string, any, error) {
	u := t.k.Upgrade
	done := map[int]bool{}
	for _, n := range in.Done {
		if n >= 1 && n <= u.Count {
			done[n] = true
		}
	}
	link := t.k.UpgradeLink(done, in.From, in.To, in.Window)
	pct := int(jsRound(float64(len(done)) / float64(u.Count) * 100))
	out := upgradeOut{Total: u.Count, Done: len(done), Percent: pct, URL: link, Help: t.k.Help}
	var b strings.Builder
	b.WriteString("Чек-лист обновления Directum RX (drxinfra.ru)")
	from, to := strings.TrimSpace(kb.CleanField(in.From)), strings.TrimSpace(kb.CleanField(in.To))
	if from != "" || to != "" {
		fmt.Fprintf(&b, "\nВерсия: %s → %s", or(from, "?"), or(to, "?"))
	}
	if w := strings.TrimSpace(kb.CleanField(in.Window)); w != "" {
		b.WriteString("\nОкно работ: " + w)
	}
	if len(done) > 0 {
		if len(done) == u.Count {
			b.WriteString("\nВсё отмечено. Запишите, что пошло не по плану.")
		} else {
			fmt.Fprintf(&b, "\nОтмечено %d из %d (%d %%).", len(done), u.Count, pct)
		}
	}
	for _, ph := range u.Phases {
		if in.Phase != 0 && ph.N != in.Phase {
			continue
		}
		po := upgradePhaseOut{N: ph.N, Title: ph.Title, Intro: ph.Intro}
		fmt.Fprintf(&b, "\n\n## %d · %s\n%s", ph.N, ph.Title, ph.Intro)
		for _, it := range ph.Items {
			po.Items = append(po.Items, upgradeItemOut{N: it.N, Text: it.Text, Note: it.Note, Optional: it.Optional, Done: done[it.N]})
			mark := "[ ]"
			if done[it.N] {
				mark = "[x]"
			}
			fmt.Fprintf(&b, "\n%s %d. %s", mark, it.N, it.Text)
			if it.Optional {
				b.WriteString(" (если есть)")
			}
			if it.Note != "" {
				b.WriteString(" — " + it.Note)
			}
		}
		out.Phases = append(out.Phases, po)
	}
	b.WriteString(t.footer(link))
	return b.String(), out, nil
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// ---------------------------------------------------------------- security_self_check

type securityIn struct {
	Answers map[string]string `json:"answers"`
}

type questionOut struct {
	ID       string  `json:"id"`
	Severity string  `json:"severity"`
	Weight   float64 `json:"weight"`
	Text     string  `json:"text"`
	Hint     string  `json:"hint,omitempty"`
	Fix      string  `json:"fix"`
	Link     string  `json:"link,omitempty"`
	Answer   string  `json:"answer,omitempty"`
}

type securityOut struct {
	Questions []questionOut  `json:"questions,omitempty"`
	Score     *int           `json:"score,omitempty"`
	Zone      string         `json:"zone,omitempty"`
	Answered  int            `json:"answered"`
	Counts    map[string]int `json:"counts,omitempty"`
	CritOpen  int            `json:"critical_open"`
	Gaps      []questionOut  `json:"gaps,omitempty"`
	URL       string         `json:"url"`
	Help      string         `json:"help"`
}

func (t *toolset) addSecurity(s *mcp.Server) {
	sec := t.k.Security
	props := map[string]*jsonschema.Schema{}
	var order []string
	for _, q := range sec.Questions {
		props[q.ID] = &jsonschema.Schema{Type: "string", Enum: []any{"y", "p", "n", "u"}}
		order = append(order, q.ID)
	}
	answers := &jsonschema.Schema{Type: "object", Properties: props, PropertyOrder: order,
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
		Description:          "Answers by question id: y = yes, p = partly, n = no, u = don't know. Unanswered questions do not count."}
	add(s, &mcp.Tool{
		Name:  "security_self_check",
		Title: "Directum RX security self-assessment",
		Description: fmt.Sprintf("The drxinfra.ru security self-assessment of a Directum RX installation: %d weighted questions on "+
			"backups, passwords, network exposure, updates, TLS, accounts, logs and more, from critical to low. Call without "+
			"answers to get the questions with weights, hints and fixes; ask the user, then call with answers to get the score "+
			"out of 100, the zone (red, yellow, green) and the gaps to close first, counted exactly as on "+
			"drxinfra.ru/security-check.html, with a link that opens the same answers there.", len(sec.Questions)),
		InputSchema: obj(map[string]*jsonschema.Schema{"answers": answers}, []string{"answers"}),
	}, t.security)
}

func (t *toolset) security(_ context.Context, in securityIn) (string, any, error) {
	sec := t.k.Security
	label := func(sev string) string {
		if l := sec.SeverityLabels[sev]; l != "" {
			return l
		}
		return sev
	}
	answers := map[string]string{}
	for k, v := range in.Answers {
		if _, ok := kb.AnswerLabels[v]; !ok {
			return "", nil, fmt.Errorf("answer %s=%q: use y, p, n or u", k, v)
		}
		answers[k] = v
	}
	qo := func(q *kb.Question, a string) questionOut {
		return questionOut{ID: q.ID, Severity: q.Severity, Weight: q.Weight, Text: q.Text, Hint: q.Hint, Fix: q.Fix, Link: q.Href, Answer: a}
	}
	var b strings.Builder
	if len(answers) == 0 {
		out := securityOut{URL: sec.Page, Help: t.k.Help}
		fmt.Fprintf(&b, "Самооценка безопасности инфраструктуры Directum RX (drxinfra.ru): %d %s. Ответы: y — да, p — частично, n — нет, u — не знаю.\n",
			len(sec.Questions), plural(len(sec.Questions), "вопрос", "вопроса", "вопросов"))
		b.WriteString("Балл = сумма весов отвеченных вопросов с множителем (да 1, частично 0,5, нет и не знаю 0) / сумма их весов × 100. ")
		b.WriteString("Красная зона: балл ниже 50 или по критичному вопросу «нет» или «не знаю»; жёлтая: ниже 80; зелёная: 80 и выше.")
		last := ""
		for i := range sec.Questions {
			q := &sec.Questions[i]
			out.Questions = append(out.Questions, qo(q, ""))
			if q.Severity != last {
				fmt.Fprintf(&b, "\n\n## %s (вес %s)", label(q.Severity), n1(q.Weight))
				last = q.Severity
			}
			fmt.Fprintf(&b, "\n%s. %s", q.ID, q.Text)
			if q.Hint != "" {
				b.WriteString(" — " + q.Hint)
			}
		}
		b.WriteString("\n\nПередайте ответы в answers, чтобы получить балл, зону и список того, что закрыть первым.")
		b.WriteString(t.footer(sec.Page))
		return b.String(), out, nil
	}
	known := map[string]bool{}
	for _, q := range sec.Questions {
		known[q.ID] = true
	}
	var unknown []string
	for k := range answers {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return "", nil, fmt.Errorf("unknown question ids: %s", strings.Join(unknown, ", "))
	}
	r := t.k.ScoreSecurity(answers)
	out := securityOut{Score: r.Score, Zone: r.Zone, Answered: r.Done, Counts: r.Counts, CritOpen: r.CritOpen, URL: r.Link, Help: t.k.Help}
	b.WriteString("Самооценка безопасности инфраструктуры Directum RX (drxinfra.ru)\n")
	if r.Score != nil {
		fmt.Fprintf(&b, "Итог: %d из 100, %s. Отвечено %d из %d.", *r.Score, strings.ToLower(kb.ZoneLabels[r.Zone]), r.Done, len(sec.Questions))
	}
	fmt.Fprintf(&b, "\nДа %d, частично %d, нет %d, не знаю %d.", r.Counts["y"], r.Counts["p"], r.Counts["n"], r.Counts["u"])
	if r.CritOpen > 0 {
		fmt.Fprintf(&b, " Открытых критичных: %d.", r.CritOpen)
	}
	if len(r.Gaps) == 0 {
		b.WriteString("\n\nОткрытых пунктов нет. Проверьте ответы фактами на серверах: опросник не заменяет проверку.")
	} else {
		b.WriteString("\n\nЧто закрыть, по порядку:")
		last := ""
		for _, g := range r.Gaps {
			out.Gaps = append(out.Gaps, qo(g.Q, g.Answer))
			if g.Q.Severity != last {
				fmt.Fprintf(&b, "\n\n%s:", label(g.Q.Severity))
				last = g.Q.Severity
			}
			fmt.Fprintf(&b, "\n- [%s] %s\n  Что сделать: %s", kb.AnswerLabels[g.Answer], g.Q.Text, g.Q.Fix)
			if g.Q.Href != "" {
				fmt.Fprintf(&b, " %s: %s", or(g.Q.Ref, "Подробнее"), g.Q.Href)
			}
		}
	}
	b.WriteString(t.footer(r.Link))
	return b.String(), out, nil
}
