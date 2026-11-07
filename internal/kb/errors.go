package kb

import (
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/dlclark/regexp2/v2"
)

// MaxLogChars is how much of the pasted text the error matcher looks at.
const MaxLogChars = 16000

// Matching follows assets/errors.js of the site: every pattern of an error is
// a JavaScript RegExp with the "i" flag; the error scores the length of its
// longest first match, and errors are ranked by that length (a longer match is
// a more specific article). Without any match, the site falls back to words of
// the text found in the error titles.
//
// Patterns are compiled with Go's RE2 engine (linear time, safe on untrusted
// input). The few that RE2 does not support (lookarounds, backreferences,
// \uXXXX) go to regexp2 in ECMAScript mode with a match timeout.

type matcher interface {
	// firstLen returns the length of the leftmost match in UTF-16 code units,
	// as m[0].length in JavaScript, or 0 when there is no match.
	firstLen(s string) int
}

type re2Matcher struct{ re *regexp.Regexp }

func (m re2Matcher) firstLen(s string) int {
	loc := m.re.FindStringIndex(s)
	if loc == nil {
		return 0
	}
	return utf16Len(s[loc[0]:loc[1]])
}

type backtrackMatcher struct{ re *regexp2.Regexp }

func (m backtrackMatcher) firstLen(s string) int {
	mt, err := m.re.FindStringMatch(s)
	if err != nil || mt == nil { // a timeout counts as no match
		return 0
	}
	return utf16Len(mt.String())
}

type rule struct {
	e   *Error
	res []matcher
	hay string // lower-case title and summary for the word fallback
}

func compileRules(errs []Error) ([]rule, error) {
	rules := make([]rule, 0, len(errs))
	for i := range errs {
		e := &errs[i]
		r := rule{e: e, hay: strings.ToLower(e.Title + " " + e.What)}
		for _, p := range e.Patterns {
			if m := compilePattern(p); m != nil {
				r.res = append(r.res, m)
			}
		}
		rules = append(rules, r)
	}
	return rules, nil
}

func compilePattern(p string) matcher {
	if re, err := regexp.Compile("(?i)" + p); err == nil {
		return re2Matcher{re}
	}
	re, err := regexp2.Compile(p, regexp2.ECMAScript|regexp2.IgnoreCase, regexp2.OptionMaxBacktrackingStackSize(20000))
	if err != nil {
		return nil // the site drops such a pattern too
	}
	re.MatchTimeout = 50 * time.Millisecond
	return backtrackMatcher{re}
}

// PatternStats counts patterns by engine; used by tests and logs.
func (k *KB) PatternStats() (re2, backtrack, dropped int) {
	for _, e := range k.Errors {
		for _, p := range e.Patterns {
			switch compilePattern(p).(type) {
			case re2Matcher:
				re2++
			case backtrackMatcher:
				backtrack++
			default:
				dropped++
			}
		}
	}
	return
}

// ErrorHit is one error page found for a piece of log.
type ErrorHit struct {
	Error *Error
	// MatchLen is the length of the matched fragment (0 for the word fallback).
	MatchLen int
	// ByWords is true when no pattern matched and the error was found by words.
	ByWords bool
}

// trimSet is what JavaScript String.prototype.trim removes, in short:
// ASCII whitespace, no-break space, BOM and the two Unicode line separators.
var trimSet = " \t\n\v\f" + string(rune(0x00a0)) + string(rune(0xfeff)) + string(rune(0x2028)) + string(rune(0x2029))

// NormalizeLog prepares pasted text the way the browser textarea does:
// line breaks become \n, surrounding whitespace is trimmed, the length is capped.
// It reports whether the text was cut.
func NormalizeLog(text string) (string, bool) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.Trim(text, trimSet)
	if r := []rune(text); len(r) > MaxLogChars {
		return string(r[:MaxLogChars]), true
	}
	return text, false
}

var wordSplit = regexp.MustCompile(`[^a-zа-яё0-9_]+`)

// FindErrors returns error pages for a log line or error text, best first,
// exactly as the search box of drxinfra.ru/errors/ ranks them. The text must
// be normalized with NormalizeLog.
func (k *KB) FindErrors(text string) []ErrorHit {
	if utf16Len(text) < 4 {
		return nil
	}
	var hits []ErrorHit
	for i := range k.rules {
		r := &k.rules[i]
		best := 0
		for _, m := range r.res {
			if n := m.firstLen(text); n > best {
				best = n
			}
		}
		if best > 0 {
			hits = append(hits, ErrorHit{Error: r.e, MatchLen: best})
		}
	}
	if len(hits) > 0 {
		// Array.prototype.sort is stable: equal lengths keep the reference order.
		stableSort(hits, func(a, b ErrorHit) bool { return a.MatchLen > b.MatchLen })
		return hits
	}
	var words []string
	for _, w := range wordSplit.Split(strings.ToLower(text), -1) {
		if utf16Len(w) > 4 {
			words = append(words, w)
			if len(words) == 12 {
				break
			}
		}
	}
	for i := range k.rules {
		r := &k.rules[i]
		n := 0
		for _, w := range words {
			if strings.Contains(r.hay, w) {
				n++
			}
		}
		if n >= 2 {
			hits = append(hits, ErrorHit{Error: r.e, ByWords: true})
		}
	}
	return hits
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if l := utf16.RuneLen(r); l > 0 {
			n += l
		} else {
			n++
		}
	}
	return n
}

func stableSort[T any](s []T, less func(a, b T) bool) {
	sort.SliceStable(s, func(i, j int) bool { return less(s[i], s[j]) })
}
