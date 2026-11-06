package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/speech"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// ---- one part of a memo ----
//
// The person drags across the recording to pick a stretch of it (a part of a
// long meeting, say) and asks what was said there. Only the words in that
// stretch go to the AI, with who said them in a meeting. Nothing is saved.

const aiPart = `You sum up one part of a longer voice recording, written out by speech recognition.

Rules:
- Write in the language of the recording (Bosnian, Croatian and Serbian in Latin letters). Never translate.
- In a meeting, lines start with who spoke: "You:" is the person you are writing for, "Others:" the other people on the call. Speak to the person as "you".
- "title": what this part is about, at most 6 words, no full stop.
- "summary": two to four plain sentences: what was discussed in this part and where it got to.
- "points": up to five short lines worth remembering: decisions, things the person said they would do or was asked to do, numbers, names. Empty when there are none.
- Use only what is in the part; never invent.
Answer with JSON only: {"title":"","summary":"","points":[]}`

type partOut struct {
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Points  []string `json:"points"`
}

// partSegments are the segments that overlap [from, to).
func partSegments(segs []store.Segment, from, to int64) []store.Segment {
	var out []store.Segment
	for _, sg := range segs {
		if sg.End > from && sg.Start < to && strings.TrimSpace(sg.Text) != "" {
			out = append(out, sg)
		}
	}
	return out
}

// partText writes the segments as lines with their time and, in a meeting,
// who spoke: "[12:30] You: …".
func partText(segs []store.Segment) string {
	var b strings.Builder
	cur := "\x00"
	for _, sg := range segs {
		t := strings.TrimSpace(sg.Text)
		if sg.Speaker != cur {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString("[" + clock(sg.Start) + "] ")
			if sg.Speaker != "" {
				b.WriteString(sg.Speaker + ": ")
			}
			cur = sg.Speaker
		} else {
			b.WriteString(" ")
		}
		b.WriteString(t)
	}
	return b.String()
}

// handlePart sums up the stretch from..to (milliseconds) of a memo.
func (s *Server) handlePart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	m, err := s.store.GetMemo(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "memo")
		return
	}
	var in struct {
		From int64 `json:"from"`
		To   int64 `json:"to"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.From < 0 || in.To <= in.From {
		writeErr(w, http.StatusBadRequest, "pick a part: from is before to")
		return
	}
	segs := partSegments(m.Segments, in.From, in.To)
	if len(segs) == 0 {
		writeErr(w, http.StatusBadRequest, "nothing was said in this part")
		return
	}
	st, _ := s.store.GetSettings(ctx, uid)
	cl, used, why := s.aiFor(ctx, uid, st)
	if cl == nil {
		writeErr(w, http.StatusConflict, firstNonEmpty(why, "no AI is connected (Settings → AI)"))
		return
	}
	text := partText(segs)
	if utf8.RuneCountInString(text) > 24000 {
		text = string([]rune(text)[:24000])
	}
	var b strings.Builder
	if m.Title != "" {
		fmt.Fprintf(&b, "The whole recording: %q, %s long.\n", m.Title, clock(m.DurationMS))
	}
	if m.Summary != "" {
		fmt.Fprintf(&b, "What the whole recording is about: %s\n", excerpt(m.Summary, 800))
	}
	fmt.Fprintf(&b, "\nThe part, from %s to %s:\n%s", clock(in.From), clock(in.To), text)

	system := aiPart
	if !m.Meeting {
		system = strings.Replace(system, "- In a meeting, lines start with who spoke: \"You:\" is the person you are writing for, \"Others:\" the other people on the call. Speak to the person as \"you\".\n", "- It is the person's own memo: speak to them as \"you\".\n", 1)
	}
	actx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var out partOut
	if err := cl.JSON(actx, system, b.String(), &out); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	out.Title = cleanLine(out.Title, 80)
	out.Summary = strings.TrimSpace(out.Summary)
	points := []string{}
	for _, p := range out.Points {
		if p = cleanLine(strings.TrimLeft(p, "-•* "), 240); p != "" && len(points) < 5 {
			points = append(points, p)
		}
	}
	if st.Latin && latinLanguage(m.Language) {
		out.Title, out.Summary = speech.ToLatin(out.Title), speech.ToLatin(out.Summary)
		for i := range points {
			points[i] = speech.ToLatin(points[i])
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": in.From, "to": in.To, "title": out.Title, "summary": out.Summary, "points": points, "provider": used})
}
