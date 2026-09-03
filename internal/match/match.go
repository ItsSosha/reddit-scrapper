// Package match decides which posts are interesting.
package match

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ItsSosha/reddit-scrapper/internal/config"
	"github.com/ItsSosha/reddit-scrapper/internal/reddit"
)

// Matcher tests posts against a set of term rules. It is safe for concurrent
// use once built.
type Matcher struct {
	allOf  []term
	anyOf  []term
	noneOf []term
	fields []string
}

type term struct {
	text string
	re   *regexp.Regexp
}

// Result describes why a post matched, so notifications can say which terms
// fired rather than just "something matched".
type Result struct {
	Matched bool
	Terms   []string
}

// New compiles a Matcher from configuration.
func New(m config.Match) (*Matcher, error) {
	compile := func(list []string) ([]term, error) {
		out := make([]term, 0, len(list))
		for _, raw := range list {
			t := strings.TrimSpace(raw)
			if t == "" {
				continue
			}
			re, err := compileTerm(t, m.CaseSensitive, m.WholeWord)
			if err != nil {
				return nil, err
			}
			out = append(out, term{text: t, re: re})
		}
		return out, nil
	}

	all, err := compile(m.AllOf)
	if err != nil {
		return nil, err
	}
	any, err := compile(m.AnyOf)
	if err != nil {
		return nil, err
	}
	none, err := compile(m.NoneOf)
	if err != nil {
		return nil, err
	}

	fields := m.Fields
	if len(fields) == 0 {
		fields = []string{"title", "selftext"}
	}
	return &Matcher{allOf: all, anyOf: any, noneOf: none, fields: fields}, nil
}

// compileTerm turns a search term into a regexp. Terms are literal text, not
// user-supplied patterns, so they are quoted; whole-word mode wraps them in
// boundaries that also tolerate phrases with punctuation.
func compileTerm(t string, caseSensitive, wholeWord bool) (*regexp.Regexp, error) {
	pattern := regexp.QuoteMeta(t)
	if wholeWord {
		if leadingWord(t) {
			pattern = `\b` + pattern
		}
		if trailingWord(t) {
			pattern += `\b`
		}
	}
	if !caseSensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("compile term %q: %w", t, err)
	}
	return re, nil
}

// leadingWord/trailingWord keep \b off terms that start or end with anything
// Go's regexp does not treat as a word character, where a boundary could never
// match: punctuation ("sold out!"), currency symbols ("£50") and non-ASCII
// letters alike, since \b here is ASCII-only.
func leadingWord(t string) bool  { return len(t) > 0 && isWordByte(t[0]) }
func trailingWord(t string) bool { return len(t) > 0 && isWordByte(t[len(t)-1]) }

func isWordByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// Match tests a post and reports the terms that fired.
func (m *Matcher) Match(p reddit.Post) Result {
	haystack := m.haystack(p)

	for _, t := range m.noneOf {
		if t.re.MatchString(haystack) {
			return Result{}
		}
	}

	var hits []string
	for _, t := range m.allOf {
		if !t.re.MatchString(haystack) {
			return Result{}
		}
		hits = append(hits, t.text)
	}

	if len(m.anyOf) > 0 {
		var anyHit bool
		for _, t := range m.anyOf {
			if t.re.MatchString(haystack) {
				anyHit = true
				hits = append(hits, t.text)
			}
		}
		if !anyHit {
			return Result{}
		}
	}

	return Result{Matched: true, Terms: hits}
}

// haystack joins the configured fields with newlines so a term cannot match
// across a field boundary.
func (m *Matcher) haystack(p reddit.Post) string {
	parts := make([]string, 0, len(m.fields))
	for _, f := range m.fields {
		if v := p.Field(f); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, "\n")
}
