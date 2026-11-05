package server

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/speech"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// The dictionary at work: its words go into the speech model's prompt next to
// the vocabulary setting, and its replacements are made in what was written
// out (memos and POST /transcribe) and in polished dictation.

// replacer makes a person's replacements: whole words and phrases, whatever
// their capitals, written as the replacement is written.
type replacer struct {
	rules []replaceRule
}

type replaceRule struct {
	id   int64
	re   *regexp.Regexp
	with string
}

func newReplacer(words []store.Word) *replacer {
	r := &replacer{}
	list := make([]store.Word, 0, len(words))
	for _, w := range words {
		if strings.TrimSpace(w.ReplaceWith) != "" && strings.TrimSpace(w.Phrase) != "" {
			list = append(list, w)
		}
	}
	// longer phrases first: "pocket ai voice" before "pocket ai"
	sort.SliceStable(list, func(i, j int) bool {
		return utf8.RuneCountInString(list[i].Phrase) > utf8.RuneCountInString(list[j].Phrase)
	})
	for _, w := range list {
		parts := strings.Fields(w.Phrase)
		for i, p := range parts {
			parts[i] = regexp.QuoteMeta(p)
		}
		re, err := regexp.Compile(`(?i)` + strings.Join(parts, `\s+`))
		if err != nil {
			continue
		}
		r.rules = append(r.rules, replaceRule{id: w.ID, re: re, with: strings.TrimSpace(w.ReplaceWith)})
	}
	return r
}

func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// apply makes the replacements in text and counts each one that fired in uses (may be nil).
func (r *replacer) apply(text string, uses map[int64]int) string {
	if r == nil {
		return text
	}
	for _, rule := range r.rules {
		locs := rule.re.FindAllStringIndex(text, -1)
		if len(locs) == 0 {
			continue
		}
		var b strings.Builder
		last, n := 0, 0
		for _, l := range locs {
			before, _ := utf8.DecodeLastRuneInString(text[:l[0]])
			after, _ := utf8.DecodeRuneInString(text[l[1]:])
			if (l[0] > 0 && wordRune(before)) || (l[1] < len(text) && wordRune(after)) {
				continue // part of a longer word
			}
			if text[l[0]:l[1]] == rule.with {
				continue // already right
			}
			b.WriteString(text[last:l[0]])
			b.WriteString(rule.with)
			last = l[1]
			n++
		}
		if n == 0 {
			continue
		}
		b.WriteString(text[last:])
		text = b.String()
		if uses != nil {
			uses[rule.id] += n
		}
	}
	return text
}

// dictionary is a person's words and their replacer.
func (s *Server) dictionary(ctx context.Context, uid int64) ([]store.Word, *replacer) {
	words, err := s.store.Words(ctx, uid)
	if err != nil {
		return nil, nil
	}
	return words, newReplacer(words)
}

// speechPrompt is what the speech model is told to expect: the vocabulary
// setting and the dictionary's words (a replacement's own spelling).
func speechPrompt(vocabulary string, words []store.Word) string {
	seen := map[string]bool{}
	var parts []string
	add := func(v string) {
		v = strings.Join(strings.Fields(v), " ")
		if v != "" && !seen[strings.ToLower(v)] {
			seen[strings.ToLower(v)] = true
			parts = append(parts, v)
		}
	}
	for _, v := range strings.FieldsFunc(vocabulary, func(r rune) bool { return r == ',' || r == '\n' || r == ';' }) {
		add(v)
	}
	for _, w := range words {
		add(firstNonEmpty(strings.TrimSpace(w.ReplaceWith), w.Phrase))
	}
	return vocabularyPrompt(strings.Join(parts, ", "))
}

// applyToResult makes the replacements in a written-out result.
func applyToResult(res *speech.Result, rp *replacer, uses map[int64]int) {
	if res == nil || rp == nil || len(rp.rules) == 0 {
		return
	}
	res.Text = rp.apply(res.Text, uses)
	for i := range res.Segments {
		res.Segments[i].Text = rp.apply(res.Segments[i].Text, nil)
	}
}
