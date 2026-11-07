package kb

import (
	"fmt"
	"math"
	"strings"
)

// The upgrade checklist and the security self-assessment are counted in the
// browser on the site (assets/upgrade.js, assets/seccheck.js). The functions
// below repeat that logic, so the link they build opens the same marks and the
// same score on drxinfra.ru.

// UpgradeLink encodes checked items (numbers 1..count) like upgrade.js:
// ?d=<bits as hex, trailing zeros cut>&from=&to=&when=.
func (k *KB) UpgradeLink(done map[int]bool, from, to, when string) string {
	n := k.Upgrade.Count
	bits := make([]int, n)
	for i := 0; i < n; i++ {
		if done[i+1] {
			bits[i] = 1
		}
	}
	var sb strings.Builder
	for i := 0; i < n; i += 4 {
		v := 0
		for j := 0; j < 4; j++ {
			v <<= 1
			if i+j < n {
				v |= bits[i+j]
			}
		}
		sb.WriteString(fmt.Sprintf("%x", v))
	}
	d := strings.TrimRight(sb.String(), "0")
	var q []string
	if d != "" {
		q = append(q, "d="+d)
	}
	for _, kv := range [][2]string{{"from", from}, {"to", to}, {"when", when}} {
		if v := strings.TrimSpace(CleanField(kv[1])); v != "" {
			q = append(q, kv[0]+"="+EncodeURIComponent(v))
		}
	}
	if len(q) == 0 {
		return k.Upgrade.Page
	}
	return k.Upgrade.Page + "?" + strings.Join(q, "&")
}

// CleanField is clean() of upgrade.js: no <, > or ", at most 40 characters.
func CleanField(s string) string {
	s = strings.NewReplacer("<", "", ">", "", `"`, "").Replace(s)
	if r := []rune(s); len(r) > 40 {
		s = string(r[:40])
	}
	return s
}

// EncodeURIComponent escapes like JavaScript's encodeURIComponent.
func EncodeURIComponent(s string) string {
	const keep = "-_.!~*'()"
	var sb strings.Builder
	for _, b := range []byte(s) {
		switch {
		case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9', strings.IndexByte(keep, b) >= 0:
			sb.WriteByte(b)
		default:
			fmt.Fprintf(&sb, "%%%02X", b)
		}
	}
	return sb.String()
}

// Answer values of the self-assessment, as in seccheck.js.
var answerValue = map[string]float64{"y": 1, "p": 0.5, "n": 0, "u": 0}

// AnswerLabels are the button captions of the self-assessment.
var AnswerLabels = map[string]string{"y": "Да", "p": "Частично", "n": "Нет", "u": "Не знаю"}

// ZoneLabels are the verdicts of the self-assessment.
var ZoneLabels = map[string]string{
	"red":    "Красная зона: начинать сейчас",
	"yellow": "Жёлтая зона: есть заметные дыры",
	"green":  "Зелёная зона: база крепкая",
}

// SecurityGap is a question not answered «Да».
type SecurityGap struct {
	Q      *Question
	Answer string
}

// SecurityScore is calc() of seccheck.js.
type SecurityScore struct {
	Score    *int // nil without answers
	Zone     string
	Done     int
	Counts   map[string]int
	CritOpen int
	Gaps     []SecurityGap
	Link     string
}

// ScoreSecurity counts the answers (question id -> y, p, n or u) like the site.
func (k *KB) ScoreSecurity(answers map[string]string) SecurityScore {
	r := SecurityScore{Counts: map[string]int{"y": 0, "p": 0, "n": 0, "u": 0}}
	got, max := 0.0, 0.0
	var link strings.Builder
	for i := range k.Security.Questions {
		q := &k.Security.Questions[i]
		a := answers[q.ID]
		if _, ok := answerValue[a]; !ok {
			link.WriteByte('-')
			continue
		}
		link.WriteString(a)
		r.Done++
		r.Counts[a]++
		max += q.Weight
		got += q.Weight * answerValue[a]
		if a != "y" {
			r.Gaps = append(r.Gaps, SecurityGap{Q: q, Answer: a})
		}
		if q.Severity == "crit" && (a == "n" || a == "u") {
			r.CritOpen++
		}
	}
	stableSort(r.Gaps, func(x, y SecurityGap) bool {
		if x.Q.Weight != y.Q.Weight {
			return x.Q.Weight > y.Q.Weight
		}
		return answerValue[x.Answer] < answerValue[y.Answer]
	})
	if max > 0 {
		s := int(math.Floor(got/max*100 + 0.5)) // Math.round
		r.Score = &s
		switch {
		case r.CritOpen > 0 || s < 50:
			r.Zone = "red"
		case s < 80:
			r.Zone = "yellow"
		default:
			r.Zone = "green"
		}
	}
	a := strings.TrimRight(link.String(), "-")
	r.Link = k.Security.Page
	if a != "" {
		r.Link += "?a=" + a
	}
	return r
}
