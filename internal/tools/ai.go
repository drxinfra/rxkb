package tools

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/drxinfra/rxkb/internal/kb"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type sizeAIIn struct {
	Model   string  `json:"model"`
	HW      string  `json:"hw"`
	Toolset string  `json:"toolset"`
	KV      string  `json:"kv"`
	Users   float64 `json:"users"`
	Share   float64 `json:"share"`
	QPH     float64 `json:"qph"`
	Target  float64 `json:"target"`
}

type aiPlanOut struct {
	Servers            int     `json:"servers"`
	ConcurrentAnswers  int     `json:"concurrent_answers"`
	AnswerP50Sec       float64 `json:"answer_p50_sec"`
	AnswerP95Sec       float64 `json:"answer_p95_sec"`
	Measured           bool    `json:"measured_on_this_server"`
	AnswersPerHour     float64 `json:"answers_per_hour_per_server"`
	FitsTarget         bool    `json:"fits_target"`
	RAMGB              float64 `json:"ram_gb"`
	RAMPerServerGB     float64 `json:"ram_per_server_gb"`
	FirstAnswerColdSec float64 `json:"first_answer_after_restart_sec,omitempty"`
	Note               string  `json:"note,omitempty"`
}

type aiQualityOut struct {
	Passed       float64   `json:"passed"`
	Attempts     float64   `json:"attempts"`
	CI95         []float64 `json:"ci95"`
	Tools        float64   `json:"tools"`
	OtherToolset bool      `json:"measured_with_other_toolset"`
}

type aiModelRow struct {
	Model          string  `json:"model"`
	Title          string  `json:"title"`
	Passed         string  `json:"passed"`
	AnswerP50Sec   float64 `json:"answer_p50_sec,omitempty"`
	AnswersPerHour float64 `json:"answers_per_hour_per_server,omitempty"`
	Servers        int     `json:"servers,omitempty"`
	RAMPerServerGB float64 `json:"ram_per_server_gb,omitempty"`
	Note           string  `json:"note,omitempty"`
}

type sizeAIOut struct {
	Input         sizeAIIn      `json:"input"`
	DemandPerHour float64       `json:"questions_per_peak_hour"`
	Plan          *aiPlanOut    `json:"plan"`
	Quality       *aiQualityOut `json:"quality,omitempty"`
	Models        []aiModelRow  `json:"all_models_on_this_server"`
	DataUpdated   string        `json:"data_updated"`
	URL           string        `json:"url"`
	Help          string        `json:"help"`
}

func choiceValues(cs []kb.AIChoice) []any {
	out := make([]any, len(cs))
	for i, c := range cs {
		out[i] = c.Value
	}
	return out
}

func (t *toolset) addSizeAI(s *mcp.Server) {
	m := t.k.AI()
	d := m.Data()
	var models, hws []string
	modelVals := make([]any, 0, len(m.Models))
	for _, k := range m.Models {
		modelVals = append(modelVals, k)
		models = append(models, k+" = "+d.Data.Models[k].Title)
	}
	hwVals := make([]any, 0, len(d.HardwareOrder))
	for _, k := range d.HardwareOrder {
		hwVals = append(hwVals, k)
		hws = append(hws, k+" = "+d.Data.Hardware[k].Title)
	}
	def := func(k string, fallback float64) float64 {
		if v, ok := d.Defaults[k].(float64); ok {
			return v
		}
		return fallback
	}
	defS := func(k string) any { return d.Defaults[k] }
	var toolsets []string
	for _, c := range d.Choices["toolset"] {
		toolsets = append(toolsets, c.Value+" = "+c.Label)
	}
	add(s, &mcp.Tool{
		Name:  "size_ai",
		Title: "Hardware for a local AI assistant for Directum RX",
		Description: "How many CPU servers and how much memory a local LLM assistant for Directum RX needs (the model works " +
			"with RX through MCP tools), from drxinfra measurements: llama.cpp on CPU, load tests with 1, 2 and 4 concurrent " +
			"users and an answer quality check. Same model and numbers as drxinfra.ru/ai-sizing.html. Returns servers, " +
			"memory, typical and p95 answer time, answers per hour, the quality score, and a comparison of all measured " +
			"models on the chosen server.",
		InputSchema: obj(map[string]*jsonschema.Schema{
			"model":   enum("Model: "+strings.Join(models, "; "), m.Default, modelVals...),
			"hw":      enum("Measured server: "+strings.Join(hws, "; "), d.HardwareOrder[0], hwVals...),
			"toolset": enum("RX tools given to the model: "+strings.Join(toolsets, "; "), defS("toolset"), choiceValues(d.Choices["toolset"])...),
			"kv":      enum("KV cache type of the model server", defS("kv"), choiceValues(d.Choices["kv"])...),
			"users":   num("Employees with the assistant", def("users", 300), 1, 1000000, false),
			"share":   num("Percent of them asking in the peak hour, usually 10-30", def("share", 20), 1, 100, false),
			"qph":     num("Questions per hour from each of them", def("qph", 2), 1, 1000, false),
			"target":  num("Acceptable typical answer time, seconds", def("target", 60), 10, 3600, false),
		}, []string{"model", "hw", "toolset", "kv", "users", "share", "qph", "target"}),
	}, t.sizeAI)
}

func (t *toolset) sizeAI(_ context.Context, in sizeAIIn) (string, any, error) {
	m := t.k.AI()
	d := m.Data()
	if !m.Has(in.Model) {
		return "", nil, fmt.Errorf("unknown model %q", in.Model)
	}
	H, ok := d.Data.Hardware[in.HW]
	if !ok {
		return "", nil, fmt.Errorf("unknown server %q", in.HW)
	}
	// render() of aisizing.js: an empty or zero field counts as 0, the target as 60.
	target := in.Target
	if target == 0 {
		target = 60
	}
	demand := in.Users * in.Share / 100 * in.QPH
	md := d.Data.Models[in.Model]
	out := sizeAIOut{Input: in, DemandPerHour: demand, DataUpdated: d.Data.Updated, URL: d.Page, Help: t.k.Help, Models: []aiModelRow{}}
	p := m.Plan(in.Model, in.HW, in.Toolset, in.KV, target, demand)
	var b strings.Builder
	fmt.Fprintf(&b, "Железо под ИИ-помощника Directum RX по замерам drxinfra (данные от %s)\n", d.Data.Updated)
	fmt.Fprintf(&b, "Модель %s, сервер %s, инструментов: %s, KV-кэш %s\n", md.Title, H.Title, toolsetLabel(d, in.Toolset), in.KV)
	fmt.Fprintf(&b, "Пиковая нагрузка ≈ %s вопросов в час (%s сотрудников × %s%% × %s в час), цель: типичный ответ до %s\n",
		n0(demand), n1(in.Users), n1(in.Share), n1(in.QPH), sec(target))
	if p == nil {
		b.WriteString("\nДля этой пары модели и сервера замеров нет.")
	} else {
		cold, hasCold := m.ColdTTFT(in.Model, in.HW, in.KV)
		out.Plan = &aiPlanOut{Servers: p.Servers, ConcurrentAnswers: p.C, AnswerP50Sec: finite(p.Time.P50), AnswerP95Sec: finite(p.Time.P95),
			Measured: p.Time.Measured, AnswersPerHour: finite(p.Cap), FitsTarget: p.Fits, RAMGB: finite(p.RAM), RAMPerServerGB: p.RAMRound, Note: p.Time.Note}
		if hasCold {
			out.Plan.FirstAnswerColdSec = finite(cold)
		}
		conc := "одновременного"
		if p.C > 1 {
			conc = "одновременных"
		}
		fmt.Fprintf(&b, "\nИтог: серверов %d (%s), памяти на сервер %s ГБ: модель %s ГБ + KV-кэш на %d %s + система\n",
			p.Servers, H.Title, n0(p.RAMRound), n1(md.FileGB), p.C, conc)
		recalc := ""
		if !p.Time.Measured {
			recalc = " (пересчёт с другого сервера)"
		}
		fmt.Fprintf(&b, "Типичный ответ %s, 95%% ответов быстрее %s%s", sec(p.Time.P50), sec(p.Time.P95), recalc)
		if hasCold {
			fmt.Fprintf(&b, "; первый ответ после перезапуска %s", sec(cold))
		}
		fmt.Fprintf(&b, "\nОдин сервер при %d %s ответ%s даёт ≈ %s ответов в час.", p.C, map[bool]string{true: "одновременном", false: "одновременных"}[p.C == 1],
			map[bool]string{true: "е", false: "ах"}[p.C == 1], n0(p.Cap))
		if !p.Fits {
			fmt.Fprintf(&b, " Даже по одному ответ дольше цели %s: нужна модель полегче или сервер мощнее.", sec(target))
		}
		if p.Time.Note != "" {
			b.WriteString(" " + capitalize(p.Time.Note) + ".")
		}
	}
	if q := m.Quality(in.Model, in.Toolset); q != nil {
		out.Quality = &aiQualityOut{Passed: q.Passed, Attempts: q.Attempts, CI95: q.CI95, Tools: q.Tools, OtherToolset: q.Fallback}
		fmt.Fprintf(&b, "\nВерных ответов на наших вопросах: %s из %s", n0(q.Passed), n0(q.Attempts))
		if len(q.CI95) == 2 {
			fmt.Fprintf(&b, " (95%% интервал %s–%s%%)", n0(q.CI95[0]*100), n0(q.CI95[1]*100))
		}
		fmt.Fprintf(&b, ", инструментов %s", n0(q.Tools))
		if q.Fallback {
			b.WriteString(", замер с другим набором")
		}
	}
	b.WriteString("\n\nВсе модели на этом сервере:\n| Модель | Верных ответов | Ответ, p50 | Ответов в час | Серверов | Память | Заметка |\n|---|---|---|---|---|---|---|")
	for _, k := range m.Models {
		mm := d.Data.Models[k]
		row := aiModelRow{Model: k, Title: mm.Title, Note: mm.Note, Passed: "—"}
		if q := m.Quality(k, in.Toolset); q != nil {
			row.Passed = n0(q.Passed) + "/" + n0(q.Attempts)
		}
		cells := []string{mm.Title, row.Passed, "—", "—", "—", "—", mm.Note}
		if pp := m.Plan(k, in.HW, in.Toolset, in.KV, target, demand); pp != nil {
			row.AnswerP50Sec, row.AnswersPerHour, row.Servers, row.RAMPerServerGB = finite(pp.Time.P50), finite(pp.Cap), pp.Servers, pp.RAMRound
			cells[2], cells[3], cells[4], cells[5] = sec(pp.Time.P50), n0(pp.Cap), fmt.Sprint(pp.Servers), n0(pp.RAMRound)+" ГБ"
		}
		if k == in.Model {
			cells[0] = "**" + cells[0] + "**"
		}
		out.Models = append(out.Models, row)
		b.WriteString("\n| " + strings.Join(cells, " | ") + " |")
	}
	if depth := m.Bench(in.Model, in.HW, in.KV); depth != nil {
		fmt.Fprintf(&b, "\n\nСкорость %s на этом сервере, токенов в секунду (llama.cpp %s, KV-кэш %s). Глубина — сколько уже лежит в контексте: у помощника RX это промпт с описаниями инструментов, 5–8 тыс. токенов.\n| Глубина контекста | Чтение запроса | Генерация |\n|---|---|---|",
			md.Title, H.Llamacpp, in.KV)
		for _, dd := range []string{"0", "4096", "8192"} {
			if x := depth[dd]; x != nil {
				var v float64
				fmt.Sscan(dd, &v)
				fmt.Fprintf(&b, "\n| %s токенов | %s | %s |", n0(v), n1(x.PP), n1(x.TG))
			}
		}
	}
	b.WriteString("\n\nНа странице калькулятора выберите те же значения, чтобы увидеть этот расчёт.")
	b.WriteString(t.footer(d.Page))
	return b.String(), out, nil
}

func toolsetLabel(d *kb.AIData, v string) string {
	for _, c := range d.Choices["toolset"] {
		if c.Value == v {
			return strings.ToLower(c.Label)
		}
	}
	return v
}

// finite keeps the exact value (rounding happens only in the text, as on the
// site) but never puts Inf or NaN into JSON.
func finite(v float64) float64 {
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return 0
	}
	return v
}

func capitalize(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return strings.ToUpper(string(r[0])) + string(r[1:])
}
