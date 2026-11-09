package speech

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ---- hesitation sounds ----
//
// Parakeet writes down every "uh" and "um"; for dictation without the AI they
// went straight into the text. These are sounds only, never words, so taking
// them out needs no AI: "uh-huh", "mhm" (yes) and words like "ovaj" stay.
// Pocket Launcher and the Pocket AI phone app do the same with the same pattern.

var fillerRe = regexp.MustCompile(`(?i)^(?:u+h+m*|u+m+|h+m+|m{3,}|e{3,}h*|a{3,}|e+r+m+|e+h+m+|a+h+m+)$`)

// isFiller: uh, uhhh, um, uhm, hmm, mmm, eee, erm, ehm… ("mm" stays: millimetres).
func isFiller(core string) bool { return fillerRe.MatchString(core) }

// splitPunct parts a word into what is around it: `"(uh,` → `"(`, `uh`, `,`.
func splitPunct(w string) (lead, core, trail string) {
	i := strings.IndexFunc(w, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) })
	if i < 0 {
		return w, "", ""
	}
	j := strings.LastIndexFunc(w, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) })
	_, n := utf8.DecodeRuneInString(w[j:])
	return w[:i], w[i : j+n], w[j+n:]
}

func endsSentence(w string) bool {
	return strings.HasSuffix(w, ".") || strings.HasSuffix(w, "?") || strings.HasSuffix(w, "!") || strings.HasSuffix(w, "…")
}

func upperFirst(w string) string {
	lead, core, trail := splitPunct(w)
	r, n := utf8.DecodeRuneInString(core)
	if n == 0 {
		return w
	}
	return lead + string(unicode.ToUpper(r)) + core[n:] + trail
}

// DropFillers takes the hesitation sounds out of a dictated text, keeping the
// punctuation readable: "Okay, uh do it" → "Okay, do it", "Uh, so we" → "So we",
// "I think, um." → "I think.".
func DropFillers(s string) string {
	if strings.Contains(s, "\n") {
		lines := strings.Split(s, "\n")
		for i, l := range lines {
			lines[i] = DropFillers(l)
		}
		return strings.Join(lines, "\n")
	}
	words := strings.Fields(s)
	out := make([]string, 0, len(words))
	upper := false // a dropped filler began a sentence: the next word does
	dropped := false
	for _, w := range words {
		_, core, trail := splitPunct(w)
		if core == "" || !isFiller(core) || strings.Contains(core, "-") {
			if upper {
				w = upperFirst(w)
				upper = false
			}
			out = append(out, w)
			continue
		}
		dropped = true
		if r, _ := utf8.DecodeRuneInString(core); unicode.IsUpper(r) && (len(out) == 0 || endsSentence(out[len(out)-1])) {
			upper = true
		}
		// a sentence end after the filler goes to the word before it
		end := strings.TrimLeft(trail, ",;:")
		if end != "" && len(out) > 0 {
			prev := strings.TrimRight(out[len(out)-1], ",;:")
			if !endsSentence(prev) {
				prev += end
			}
			out[len(out)-1] = prev
			upper = false
		}
	}
	if !dropped {
		return s
	}
	if n := len(out); n > 0 {
		out[n-1] = strings.TrimRight(out[n-1], ",;:")
	}
	return strings.Join(out, " ")
}

// DropFillersResult takes the hesitation sounds out of a result's text and
// segments; a segment left without words goes.
func DropFillersResult(res *Result) {
	if res == nil {
		return
	}
	res.Text = DropFillers(res.Text)
	segs := res.Segments[:0]
	for _, sg := range res.Segments {
		sg.Text = DropFillers(sg.Text)
		if strings.TrimSpace(sg.Text) != "" {
			segs = append(segs, sg)
		}
	}
	res.Segments = segs
}
