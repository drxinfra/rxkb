package kb

import (
	"errors"
	"math"
)

// Hardware for a local AI assistant, as drxinfra.ru/ai-sizing.html computes
// it. The measurements (assets/aisizing-data.js) come with the bundle; the
// formulas below repeat assets/aisizing.js line by line, including its
// JavaScript fallbacks (a missing measurement falls back to f16, then q8_0).

const (
	aiCtxSlot    = 16384 // context per concurrent user: prompt with tools plus 2-3 steps
	aiOverheadGB = 2     // OS, model server, compute buffers
)

// AIData is the "aisizing" part of the bundle.
type AIData struct {
	Data          AIRaw                 `json:"data"`
	HardwareOrder []string              `json:"hardware_order"`
	ModelOrder    []string              `json:"model_order"`
	Defaults      map[string]any        `json:"defaults"`
	Choices       map[string][]AIChoice `json:"choices"`
	Page          string                `json:"page"`
}

// AIChoice is one radio button of the form.
type AIChoice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// AIRaw is window.AISIZING of the site.
type AIRaw struct {
	Updated  string                  `json:"updated"`
	Hardware map[string]AIHardware   `json:"hardware"`
	Models   map[string]*AIModelData `json:"models"`
}

// AIHardware is a measured server.
type AIHardware struct {
	CPU      string  `json:"cpu"`
	VCPU     float64 `json:"vcpu"`
	RAMGB    float64 `json:"ram_gb"`
	Llamacpp string  `json:"llamacpp"`
	Title    string  `json:"title"`
}

// AIModelData is one measured model.
type AIModelData struct {
	Title     string                `json:"title"`
	License   string                `json:"license"`
	Note      string                `json:"note"`
	Speed     map[string]*AISpeed   `json:"speed"`
	FileGB    float64               `json:"file_gb"`
	KV        map[string]float64    `json:"kv"`
	Quality   map[string]*AIQuality `json:"quality"`
	QualityHW string                `json:"quality_hw"`
	QualityKV string                `json:"quality_kv"`
}

// AISpeed holds llama-bench and load-test results of a model on one server.
type AISpeed struct {
	Bench *AIBench                       `json:"bench"`
	Serve map[string]map[string]*AIServe `json:"serve"` // kv -> cold1/warm1/warm2/warm4
}

// AIBench is llama-bench: tokens per second by context depth.
type AIBench struct {
	Depth map[string]map[string]*AIPPTG `json:"depth"` // kv -> depth -> pp/tg
}

// AIPPTG is prompt processing and generation speed, tokens per second.
type AIPPTG struct {
	PP float64 `json:"pp"`
	TG float64 `json:"tg"`
}

// AIServe is a load-test measurement, seconds.
type AIServe struct {
	TTFTP50  float64 `json:"ttft_p50"`
	TotalP50 float64 `json:"total_p50"`
}

// AIQuality is the answer quality check (drxeval) of a model.
type AIQuality struct {
	Passed     float64   `json:"passed"`
	Attempts   float64   `json:"attempts"`
	CI95       []float64 `json:"ci95"`
	Tools      float64   `json:"tools"`
	LatencyP50 float64   `json:"latency_p50"`
	LatencyP95 float64   `json:"latency_p95"`
}

// AIModel computes plans on the bundle data.
type AIModel struct {
	d       *AIData
	Models  []string // models with measurements, in the order of the form
	Default string   // model selected on the page by default
}

func newAIModel(d *AIData) (*AIModel, error) {
	m := &AIModel{d: d}
	for _, k := range d.ModelOrder {
		if md := d.Data.Models[k]; md != nil && len(md.Speed) > 0 {
			m.Models = append(m.Models, k)
		}
	}
	if len(m.Models) == 0 || len(d.HardwareOrder) == 0 {
		return nil, errors.New("нет моделей с замерами или серверов")
	}
	m.Default = m.Models[0]
	for _, k := range m.Models {
		if k == "qwen3-8b" { // aisizing.js: ms.value = 'qwen3-8b'
			m.Default = k
		}
	}
	return m, nil
}

// Data returns the bundle part the model works on.
func (m *AIModel) Data() *AIData { return m.d }

// Has reports whether the model key is one of the measured models.
func (m *AIModel) Has(model string) bool {
	for _, k := range m.Models {
		if k == model {
			return true
		}
	}
	return false
}

// AIQualityShown is quality() of aisizing.js: the toolset asked for, or the
// other one marked as a fallback.
type AIQualityShown struct {
	*AIQuality
	Fallback bool
}

func (m *AIModel) quality(md *AIModelData, toolset string) *AIQualityShown {
	if q := md.Quality[toolset]; q != nil {
		return &AIQualityShown{AIQuality: q}
	}
	if q := md.Quality["all"]; q != nil {
		return &AIQualityShown{AIQuality: q, Fallback: true}
	}
	if q := md.Quality["pilot"]; q != nil {
		return &AIQualityShown{AIQuality: q, Fallback: true}
	}
	return nil
}

// AITime is the answer time of the assistant: several model steps plus tools.
type AITime struct {
	P50, P95 float64
	Measured bool   // false when recalculated from another server
	Note     string // set when there is no load test for this concurrency
}

func serveKV(s map[string]map[string]*AIServe, kv string) map[string]*AIServe {
	if s == nil {
		return nil
	}
	for _, k := range []string{kv, "f16", "q8_0"} {
		if x := s[k]; x != nil {
			return x
		}
	}
	return nil
}

func (md *AIModelData) serve(hw string) map[string]map[string]*AIServe {
	if sp := md.Speed[hw]; sp != nil {
		return sp.Serve
	}
	return nil
}

func depthTG(md *AIModelData, hw, kv string) float64 {
	sp := md.Speed[hw]
	if sp == nil || sp.Bench == nil || sp.Bench.Depth == nil {
		return 0
	}
	b := sp.Bench.Depth
	var d map[string]*AIPPTG
	for _, k := range []string{kv, "f16", "q8_0"} {
		if b[k] != nil {
			d = b[k]
			break
		}
	}
	if d == nil || d["8192"] == nil {
		return 0
	}
	return d["8192"].TG
}

func (m *AIModel) answerTime(model, hw, toolset, kv string, c int) *AITime {
	md := m.d.Data.Models[model]
	q := m.quality(md, toolset)
	if q == nil {
		return nil
	}
	qhw := md.QualityHW
	base, base95 := q.LatencyP50, q.LatencyP95
	sHere, sQ := md.serve(hw), md.serve(qhw)
	sk, sqk := serveKV(sHere, kv), serveKV(sQ, kv)
	f, measured := 1.0, hw == qhw
	if !measured {
		// Another server: scale by generation speed at 8k tokens of context (llama-bench).
		a, b := depthTG(md, hw, kv), depthTG(md, qhw, kv)
		if a == 0 || b == 0 {
			return nil
		}
		f = b / a
	}
	// The quality check ran with one KV cache; another one is scaled by the single-user load test.
	qkv := md.QualityKV
	if qkv == "" {
		qkv = kv
	}
	if qkv != kv {
		sv := sHere
		if sv == nil {
			sv = sQ
		}
		if sv != nil && sv[kv] != nil && sv[qkv] != nil && sv[kv]["warm1"] != nil && sv[qkv]["warm1"] != nil {
			f *= sv[kv]["warm1"].TotalP50 / sv[qkv]["warm1"].TotalP50
		}
	}
	// Load test: how much slower an answer gets with c concurrent users.
	g, note := 1.0, ""
	if c > 1 {
		src := sk
		if src == nil {
			src = sqk
		}
		warm := "warm" + string(rune('0'+c))
		if src != nil && src[warm] != nil && src["warm1"] != nil {
			g = src[warm].TotalP50 / src["warm1"].TotalP50
		} else {
			g, note = float64(c), "нет замера нагрузки — считаем, что ответы идут по очереди"
		}
	}
	return &AITime{P50: base * f * g, P95: base95 * f * g, Measured: measured, Note: note}
}

// ColdTTFT is the first answer after a restart: the model reads the prompt and
// tool descriptions again. Zero when not measured.
func (m *AIModel) ColdTTFT(model, hw, kv string) (float64, bool) {
	x := serveKV(m.d.Data.Models[model].serve(hw), kv)
	if x == nil || x["cold1"] == nil {
		return 0, false
	}
	return x["cold1"].TTFTP50, true
}

// RAMGB is memory for the model file, KV cache for c users and the system.
func (m *AIModel) RAMGB(model, kv string, c int) float64 {
	md := m.d.Data.Models[model]
	perTok := md.KV[kv]
	if perTok == 0 {
		perTok = md.KV["f16"]
	}
	return md.FileGB + perTok*aiCtxSlot*float64(c)/1e9 + aiOverheadGB
}

// AIPlan is plan() of aisizing.js: the concurrency that fits the target with
// the best throughput, and the servers and memory for the demand.
type AIPlan struct {
	C        int     // concurrent answers per server
	Time     AITime  // answer time at that concurrency
	Cap      float64 // answers per hour per server
	Fits     bool    // the typical answer fits the target time
	Servers  int
	RAM      float64
	RAMRound float64 // rounded up to 8 GB
}

// Plan returns nil when there are no measurements for the model and server.
func (m *AIModel) Plan(model, hw, toolset, kv string, target, demand float64) *AIPlan {
	var best *AIPlan
	for _, c := range []int{1, 2, 4} {
		t := m.answerTime(model, hw, toolset, kv, c)
		if t == nil {
			continue
		}
		capacity := float64(c) * 3600 / t.P50
		fits := t.P50 <= target
		if best == nil || (fits && (!best.Fits || capacity > best.Cap)) {
			best = &AIPlan{C: c, Time: *t, Cap: capacity, Fits: fits}
		}
	}
	if best == nil {
		return nil
	}
	best.Servers = int(math.Max(1, math.Ceil(demand/best.Cap)))
	best.RAM = m.RAMGB(model, kv, best.C)
	best.RAMRound = math.Ceil(best.RAM/8) * 8
	return best
}

// Quality returns the quality shown for the model and toolset, or nil.
func (m *AIModel) Quality(model, toolset string) *AIQualityShown {
	return m.quality(m.d.Data.Models[model], toolset)
}

// Bench returns llama-bench speed by depth for the model on the server.
func (m *AIModel) Bench(model, hw, kv string) map[string]*AIPPTG {
	sp := m.d.Data.Models[model].Speed[hw]
	if sp == nil || sp.Bench == nil || sp.Bench.Depth == nil {
		return nil
	}
	if d := sp.Bench.Depth[kv]; d != nil {
		return d
	}
	return sp.Bench.Depth["f16"]
}
