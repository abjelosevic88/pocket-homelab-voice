package server

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// ---- names heard in the conversation ----
//
// People call each other by name: "Alex, can you…" and the next voice to
// answer is likely Alex. Once a meeting's voices are found, the person's AI
// reads the conversation (only the words) and says which unnamed voice may be
// whom and why. Those are hints next to the voice; nothing is named until the
// person says so.

const aiHints = `You read a meeting written out by speech recognition and work out who the unnamed voices are.

Lines start with the time and who spoke: "You:" is the person you help; names are voices already known; "Speaker 1:", "Speaker 2:", … are voices not named yet.
Use only clear clues in the words:
- someone addresses a person by name and that voice answers right after ("Alex, what about you?" → the next to speak is likely Alex);
- the host hands over by name ("Heidi, your turn");
- a voice says its own name, or others thank or answer them by name right after they spoke.
Never guess from a name that is merely mentioned. Each name at most once; never a name already known. When there is no clear clue for a voice, leave it out.
Answer with JSON only: {"speakers":[{"label":"Speaker 2","name":"Alex","why":"one short sentence with the time of the clue"}]}`

type hintsOut struct {
	Speakers []struct {
		Label string `json:"label"`
		Name  string `json:"name"`
		Why   string `json:"why"`
	} `json:"speakers"`
}

// hintsSoon asks the AI for names in the background.
func (s *Server) hintsSoon(uid, id int64) {
	go func() {
		ctx, cancel := context.WithTimeout(s.bg, 4*time.Minute)
		defer cancel()
		if err := s.hints(ctx, uid, id); err != nil {
			s.log.Debug("speaker hints", "memo", id, "err", err)
		}
	}()
}

func (s *Server) hints(ctx context.Context, uid, id int64) error {
	select {
	case s.aiSem <- struct{}{}:
		defer func() { <-s.aiSem }()
	case <-ctx.Done():
		return ctx.Err()
	}
	m, err := s.store.GetMemo(ctx, uid, id)
	if err != nil {
		return err
	}
	open := 0
	for _, sp := range m.Speakers {
		if sp.State == store.SpeakerUnknown || sp.State == store.SpeakerSuggested {
			open++
		}
	}
	if open == 0 {
		return nil
	}
	st, _ := s.store.GetSettings(ctx, uid)
	cl, _, why := s.aiFor(ctx, uid, st)
	if cl == nil {
		return fmt.Errorf("no AI: %s", why)
	}
	text := timedConversation(m)
	if utf8.RuneCountInString(text) > 60000 {
		text = string([]rune(text)[:60000])
	}
	var known []string
	for _, sp := range m.Speakers {
		if sp.Name != "" && sp.State != store.SpeakerYou {
			known = append(known, sp.Name)
		}
	}
	user := "The meeting:\n" + text
	if len(known) > 0 {
		user = "Voices already known: " + strings.Join(known, ", ") + ".\n\n" + user
	}
	var out hintsOut
	if err := cl.JSON(ctx, aiHints, user, &out); err != nil {
		return err
	}
	cur, err := s.store.GetMemo(ctx, uid, id)
	if err != nil {
		return err
	}
	used := map[string]bool{}
	for _, n := range known {
		used[strings.ToLower(n)] = true
	}
	n := 0
	for _, h := range out.Speakers {
		name := cleanLine(h.Name, 60)
		if name == "" || used[strings.ToLower(name)] || strings.EqualFold(name, speakerYou) {
			continue
		}
		for i := range cur.Speakers {
			sp := &cur.Speakers[i]
			if strings.EqualFold(speakerLabel(cur, sp.Key), strings.TrimSpace(h.Label)) && (sp.State == store.SpeakerUnknown || sp.State == store.SpeakerSuggested) {
				sp.Hint, sp.HintWhy = name, cleanLine(h.Why, 200)
				used[strings.ToLower(name)] = true
				n++
				break
			}
		}
	}
	if n == 0 {
		return nil
	}
	s.log.Debug("speaker hints", "memo", id, "hints", n)
	return s.store.SaveMemo(ctx, uid, cur)
}

// timedConversation is the meeting as "[12:09] Label: …" lines.
func timedConversation(m *store.Memo) string {
	var b strings.Builder
	cur := "\x00"
	for _, sg := range m.Segments {
		t := strings.TrimSpace(sg.Text)
		if t == "" {
			continue
		}
		if sg.Speaker != cur {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "[%s] %s: ", clock(sg.Start), speakerLabel(m, sg.Speaker))
			cur = sg.Speaker
		} else {
			b.WriteString(" ")
		}
		b.WriteString(t)
	}
	return b.String()
}
