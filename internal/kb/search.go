package kb

import (
	"math"
	"regexp"
	"strings"
	"unicode"
)

// Search follows assets/search.js of the site, so an agent and a person typing
// the same words into drxinfra.ru/search.html get the same pages in the same
// order: words are lower-cased with ё as е, long Russian words lose their last
// two letters, a word matches by prefix, and the title weighs more than the
// headings, the headings more than the description, the description more than
// the text.

var (
	fieldWeight = [4]float64{8, 4, 3, 1} // t, h, d, x in the order search.js sums them
	wordRe      = regexp.MustCompile(`[a-zа-я0-9_.]+`)
	cyrillicRe  = regexp.MustCompile(`^[а-я]+$`)
)

type searchDoc struct {
	d *SearchDoc
	f [4][]string
}

func normRunes(s string) []rune {
	r := []rune(s)
	for i, c := range r {
		c = unicode.ToLower(c)
		if c == 'ё' {
			c = 'е'
		}
		r[i] = c
	}
	return r
}

func searchWords(s string) []string {
	return wordRe.FindAllString(string(normRunes(s)), -1)
}

func stem(w string) string {
	r := []rune(w)
	if cyrillicRe.MatchString(w) && len(r) > 5 {
		n := len(r) - 2
		if n < 5 {
			n = 5
		}
		return string(r[:n])
	}
	return w
}

func prepareDocs(docs []SearchDoc) []searchDoc {
	out := make([]searchDoc, len(docs))
	for i := range docs {
		d := &docs[i]
		out[i].d = d
		for j, s := range [4]string{d.T, d.H, d.D, d.X} {
			ws := searchWords(s)
			for n, w := range ws {
				ws[n] = strings.TrimRight(w, ".")
			}
			out[i].f[j] = ws
		}
	}
	return out
}

// SearchTerms turns a query into search terms as search.js does.
func SearchTerms(q string) []string {
	var terms []string
	for _, w := range searchWords(q) {
		if t := stem(w); utf16Len(t) > 1 {
			terms = append(terms, t)
		}
	}
	return terms
}

// SearchHit is one page found by Search.
type SearchHit struct {
	Doc     *SearchDoc
	Score   float64
	Hit     int
	Snippet string
}

// SearchSite ranks the site pages for the query. Strict is false when no page
// has every term and the list holds the partial matches, as on the site
// («Точных совпадений нет, похожее»).
func (k *KB) SearchSite(q string) (hits []SearchHit, strict bool, total int) {
	terms := SearchTerms(q)
	if len(terms) == 0 {
		return nil, false, 0
	}
	var all, exact []SearchHit
	for i := range k.docs {
		s, hit := score(&k.docs[i], terms)
		if s <= 0 {
			continue
		}
		h := SearchHit{Doc: k.docs[i].d, Score: s, Hit: hit}
		all = append(all, h)
		if hit == len(terms) {
			exact = append(exact, h)
		}
	}
	list := all
	if len(exact) > 0 {
		list, strict = exact, true
	}
	stableSort(list, func(a, b SearchHit) bool {
		if a.Hit != b.Hit {
			return a.Hit > b.Hit
		}
		return a.Score > b.Score
	})
	total = len(list)
	if len(list) > 30 {
		list = list[:30]
	}
	for i := range list {
		list[i].Snippet = snippet(list[i].Doc, terms)
	}
	return list, strict, total
}

func score(d *searchDoc, terms []string) (total float64, hit int) {
	for _, t := range terms {
		best := 0.0
		for k := 0; k < 4; k++ {
			arr, n := d.f[k], 0
			for i := 0; i < len(arr) && n < 5; i++ {
				if strings.HasPrefix(arr[i], t) {
					n++
				}
			}
			if n > 0 {
				best += fieldWeight[k] * (1 + math.Log(float64(n)))
			}
		}
		if best != 0 {
			hit++
			total += best
		}
	}
	return total, hit
}

func snippet(d *SearchDoc, terms []string) string {
	txt := []rune(d.X)
	low := string(normRunes(d.X))
	pos := -1
	for _, t := range terms {
		re := regexp.MustCompile(`(^|[^a-zа-я0-9_])` + regexp.QuoteMeta(t))
		if loc := re.FindStringIndex(low); loc != nil {
			pos = len([]rune(low[:loc[0]]))
			break
		}
	}
	if pos < 0 {
		return d.D
	}
	a, b := pos-90, pos+170
	if a < 0 {
		a = 0
	}
	if b > len(txt) {
		b = len(txt)
	}
	var sb strings.Builder
	if a > 0 {
		sb.WriteString("… ")
	}
	sb.WriteString(string(txt[a:b]))
	if b < len(txt) {
		sb.WriteString(" …")
	}
	return sb.String()
}
