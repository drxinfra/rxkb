// Package kb holds the drxinfra.ru knowledge base that rxkb serves: the error
// reference, blog articles, the site search index, the calculators and the
// checklists. Everything comes from one kb.json built by hack/build-kb.py from
// the site sources; the server never fetches anything from the network.
package kb

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Format is the bundle format version this server understands.
const Format = 1

// MaxBundleBytes caps the size of kb.json the server agrees to load.
const MaxBundleBytes = 64 << 20

// Bundle is kb.json as written by hack/build-kb.py.
type Bundle struct {
	Format   int         `json:"format"`
	Site     string      `json:"site"`
	Help     string      `json:"help"`
	Source   string      `json:"source"`
	Errors   []Error     `json:"errors"`
	Articles []Article   `json:"articles"`
	Search   []SearchDoc `json:"search"`
	Sizing   SizingData  `json:"sizing"`
	AISizing AIData      `json:"aisizing"`
	Versions Versions    `json:"versions"`
	Upgrade  Upgrade     `json:"upgrade"`
	Security Security    `json:"security"`
	// Ports holds sha256 of the site scripts whose logic the server repeats in Go.
	Ports map[string]string `json:"ports"`
}

// portedFrom lists the versions of the site scripts the Go code was checked
// against (hack/parity). When the site changes one of them, the server still
// works but warns: the port has to be checked again.
var portedFrom = map[string]string{
	"aisizing.js": "74e150bd6d73546011646916f333637b326bbc68dfc55e182d6412d0608dadf6",
	"errors.js":   "297e4c0bd6fdc675883a838d1d8132b19e170951141e438743ccdc9349b4cc8c",
	"search.js":   "264a31db1f1029f5c125bcec639062307559739a0f58b875e385c5f86fd73061",
	"seccheck.js": "ba525b896968848d95ba34980e48f3469b6511f7a2032d2ef2d846655db19c06",
	"upgrade.js":  "d6648859eefc71b5cc94ee23447a26328178097a83a8f7013e825e9f1f3c0152",
}

// Error is one page of the error reference (errors/posts/*.md on the site).
type Error struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	What        string            `json:"what"`
	Component   string            `json:"component"`
	Date        string            `json:"date"`
	Description string            `json:"description"`
	Patterns    []string          `json:"patterns"`
	Sections    map[string]string `json:"sections"`
	Markdown    string            `json:"markdown"`
	URL         string            `json:"url"`
}

// Article is one blog post (blog/posts/*.md on the site).
type Article struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Date        string   `json:"date"`
	Updated     string   `json:"updated"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Readtime    string   `json:"readtime"`
	Markdown    string   `json:"markdown"`
	URL         string   `json:"url"`
}

// SearchDoc is one entry of the site search index (search.json).
type SearchDoc struct {
	U string `json:"u"` // absolute URL
	K string `json:"k"` // kind: Статья, Ошибка, Инструмент, Страница
	T string `json:"t"` // title
	D string `json:"d"` // description
	H string `json:"h"` // headings
	X string `json:"x"` // text
}

// SizingData carries assets/sizing.js of the site (the server runs it as is)
// and the fields of the form on sizing.html.
type SizingData struct {
	Script string                 `json:"script"`
	SHA256 string                 `json:"sha256"`
	Page   string                 `json:"page"`
	Fields map[string]SizingField `json:"fields"`
}

// SizingField is one input of the sizing form: number, radio, checkbox or select.
type SizingField struct {
	Kind    string     `json:"kind"`
	Label   string     `json:"label"`
	Min     float64    `json:"min"`
	Unit    string     `json:"unit"`
	Choices []AIChoice `json:"choices"`
}

// Versions is the component version table of rx-linux-guide.html.
type Versions struct {
	Columns []string     `json:"columns"`
	Rows    []VersionRow `json:"rows"`
	Source  string       `json:"source"`
	Notes   []string     `json:"notes"`
	Page    string       `json:"page"`
}

// VersionRow is one component with a value per column.
type VersionRow struct {
	Component string   `json:"component"`
	Values    []string `json:"values"`
}

// Upgrade is the upgrade checklist of upgrade-checklist.html.
type Upgrade struct {
	Phases []Phase `json:"phases"`
	Count  int     `json:"count"`
	Page   string  `json:"page"`
}

// Phase is one stage of the upgrade checklist.
type Phase struct {
	N     int         `json:"n"`
	Title string      `json:"title"`
	Intro string      `json:"intro"`
	Items []CheckItem `json:"items"`
}

// CheckItem is one checkbox of the upgrade checklist; N runs through all phases.
type CheckItem struct {
	N        int    `json:"n"`
	Name     string `json:"name"`
	Text     string `json:"text"`
	Note     string `json:"note"`
	Optional bool   `json:"optional"`
}

// Security is the self-assessment of security-check.html.
type Security struct {
	Questions      []Question        `json:"questions"`
	SeverityLabels map[string]string `json:"severity_labels"`
	Page           string            `json:"page"`
}

// Question is one weighted question of the security self-assessment.
type Question struct {
	ID       string  `json:"id"`
	Severity string  `json:"severity"`
	Weight   float64 `json:"weight"`
	Text     string  `json:"text"`
	Hint     string  `json:"hint"`
	Fix      string  `json:"fix"`
	Ref      string  `json:"ref"`
	Href     string  `json:"href"`
}

// KB is a loaded, validated and indexed bundle. It is immutable and safe for
// concurrent use; a reload builds a new KB.
type KB struct {
	Bundle
	Hash     string   // first 12 hex digits of sha256 of kb.json
	Warnings []string // problems that do not stop the server
	errors   map[string]*Error
	articles map[string]*Article
	rules    []rule
	docs     []searchDoc
	sizing   *SizingEngine
	ai       *AIModel
}

// LoadFile reads, validates and indexes kb.json.
func LoadFile(path string) (*KB, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxBundleBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxBundleBytes {
		return nil, fmt.Errorf("%s: больше %d МБ", path, MaxBundleBytes>>20)
	}
	return Load(raw)
}

// Load validates and indexes a bundle given as JSON.
func Load(raw []byte) (*KB, error) {
	var b Bundle
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("kb.json: %w", err)
	}
	if b.Format != Format {
		return nil, fmt.Errorf("kb.json: формат %d, сервер понимает %d", b.Format, Format)
	}
	if err := b.validate(); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	k := &KB{Bundle: b, Hash: hex.EncodeToString(sum[:])[:12],
		errors: make(map[string]*Error, len(b.Errors)), articles: make(map[string]*Article, len(b.Articles))}
	for i := range k.Errors {
		k.errors[k.Errors[i].ID] = &k.Errors[i]
	}
	for i := range k.Articles {
		k.articles[k.Articles[i].ID] = &k.Articles[i]
	}
	var err error
	if k.rules, err = compileRules(k.Errors); err != nil {
		return nil, err
	}
	k.docs = prepareDocs(k.Search)
	if k.sizing, err = NewSizingEngine(b.Sizing.Script); err != nil {
		return nil, fmt.Errorf("sizing.js: %w", err)
	}
	if k.ai, err = newAIModel(&k.AISizing); err != nil {
		return nil, fmt.Errorf("aisizing: %w", err)
	}
	for _, name := range sortedKeys(portedFrom) {
		if got := b.Ports[name]; got != portedFrom[name] {
			k.Warnings = append(k.Warnings, fmt.Sprintf("assets/%s на сайте изменился (%.12s вместо %.12s): сверьте порт в rxkb (hack/parity)", name, got, portedFrom[name]))
		}
	}
	if re2, bt, dropped := k.PatternStats(); dropped > 0 {
		k.Warnings = append(k.Warnings, fmt.Sprintf("шаблонов ошибок не скомпилировалось: %d (RE2 %d, regexp2 %d)", dropped, re2, bt))
	}
	return k, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (b *Bundle) validate() error {
	var problems []string
	need := func(ok bool, what string) {
		if !ok {
			problems = append(problems, what)
		}
	}
	need(strings.HasPrefix(b.Site, "https://"), "site")
	need(strings.HasPrefix(b.Help, "https://"), "help")
	need(len(b.Errors) > 0, "errors")
	need(len(b.Articles) > 0, "articles")
	need(len(b.Search) > 0, "search")
	need(b.Sizing.Script != "", "sizing.script")
	need(len(b.AISizing.ModelOrder) > 0, "aisizing.model_order")
	need(len(b.Versions.Rows) > 0 && len(b.Versions.Columns) > 0, "versions")
	need(b.Upgrade.Count > 0, "upgrade")
	need(len(b.Security.Questions) > 0, "security")
	seen := map[string]bool{}
	for _, e := range b.Errors {
		need(e.ID != "" && !seen["e/"+e.ID], "errors: пустой или повторный id "+e.ID)
		seen["e/"+e.ID] = true
	}
	for _, a := range b.Articles {
		need(a.ID != "" && !seen["a/"+a.ID], "articles: пустой или повторный id "+a.ID)
		seen["a/"+a.ID] = true
	}
	for _, r := range b.Versions.Rows {
		need(len(r.Values) == len(b.Versions.Columns), "versions: строка "+r.Component)
	}
	if len(problems) > 0 {
		return errors.New("kb.json: нет или неверно: " + strings.Join(problems, "; "))
	}
	return nil
}

// ErrorByID returns an error reference page by its id (slug).
func (k *KB) ErrorByID(id string) *Error { return k.errors[id] }

// ArticleByID returns a blog article by its id (slug).
func (k *KB) ArticleByID(id string) *Article { return k.articles[id] }

// Sizing returns the engine that runs the site's sizing model.
func (k *KB) Sizing() *SizingEngine { return k.sizing }

// AI returns the hardware model for a local AI assistant.
func (k *KB) AI() *AIModel { return k.ai }

// Stats is a short description of the loaded bundle for logs and /healthz.
func (k *KB) Stats() string {
	return fmt.Sprintf("kb %s (сайт %s): ошибок %d, статей %d, страниц поиска %d", k.Hash, k.Source, len(k.Errors), len(k.Articles), len(k.Search))
}
