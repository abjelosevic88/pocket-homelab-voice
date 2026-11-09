package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/speech"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// ---- meetings ----
//
// Pocket Launcher records a call as stereo: the person's microphone on the
// left ("You"), the computer's sound — the others on the call — on the right
// ("Others"). Each channel is written out on its own, with its quiet stretches
// silenced (and, on the person's side, the call heard back through the
// speakers), and the two are merged by time into one conversation.

const (
	speakerYou    = "You"
	speakerOthers = "Others"
	// a channel with less sound than this is treated as silent the whole time
	minVoicedMS = 1000
)

// writeOutMeeting writes out a meeting recording: path is the recording, mono
// the 16 kHz mono WAV made of it, run writes out one file with the person's engine.
func (s *Server) writeOutMeeting(ctx context.Context, m *store.Memo, path, mono string, run func(string, func(int)) (*speech.Result, error)) (*speech.Result, error) {
	base := filepath.Join(s.tmpDir(), fmt.Sprintf("meeting-%d-%d", m.ID, time.Now().UnixNano()))
	stereo, left, right := base+"-stereo.wav", base+"-you.wav", base+"-others.wav"
	defer os.Remove(stereo)
	defer os.Remove(left)
	defer os.Remove(right)
	single := func() (*speech.Result, error) {
		return run(mono, func(p int) { s.setProgress(m.ID, "writing", p) })
	}
	if err := s.speech.ToWAVChannels(ctx, path, stereo); err != nil {
		s.log.Warn("meeting: stereo", "memo", m.ID, "err", err)
		return single()
	}
	channels, voiced, echo, err := speech.SplitStereo(stereo, left, right)
	if err != nil || channels != 2 {
		// not stereo: one voice, written out as any memo
		return single()
	}
	if echo > 0 {
		// the call heard back through the speakers, taken off the person's side
		s.log.Info("meeting: echo removed", "memo", m.ID, "echo_s", echo/1000, "you_s", voiced[0]/1000)
	}
	type part struct {
		file, speaker string
	}
	var parts []part
	if voiced[0] >= minVoicedMS {
		parts = append(parts, part{left, speakerYou})
	}
	if voiced[1] >= minVoicedMS {
		parts = append(parts, part{right, speakerOthers})
	}
	if len(parts) == 0 {
		parts = []part{{left, speakerYou}} // all quiet: the model finds nothing, which is the answer
	}
	var all []speech.Segment
	var out *speech.Result
	for i, p := range parts {
		i, n := i, len(parts)
		res, err := run(p.file, func(pct int) { s.setProgress(m.ID, "writing", (i*100+pct)/n) })
		if err != nil {
			return nil, err
		}
		// words over a silenced stretch, or one line looped: made up
		segs := speech.DropLoops(res.Segments)
		if sound, err := speech.Sounding(p.file); err == nil {
			segs = speech.DropSilent(segs, sound)
		}
		if n := len(res.Segments) - len(segs); n > 0 {
			s.log.Info("meeting: made-up lines dropped", "memo", m.ID, "speaker", p.speaker, "lines", n)
		}
		for _, sg := range segs {
			sg.Speaker = p.speaker
			all = append(all, sg)
		}
		if out == nil || (out.Language == "" && res.Language != "") {
			lang := res.Language
			out = &speech.Result{Language: lang, Engine: res.Engine}
		}
	}
	out.Segments, out.Text = mergeSpeakers(dropEcho(all))
	if voiced[1] >= minVoicedMS {
		s.splitVoices(ctx, m, out, right)
	}
	return out, nil
}

// splitVoices tells the voices on the call apart and names them (when this
// box can); out's segments then carry S1, S2, … and its text their names.
func (s *Server) splitVoices(ctx context.Context, m *store.Memo, out *speech.Result, right string) {
	if ok, why := s.speakersAvailable(); !ok {
		s.log.Debug("meeting: voices not split", "memo", m.ID, "why", why)
		return
	}
	s.setProgress(m.ID, "speakers", -1)
	segs := make([]store.Segment, len(out.Segments))
	for i, sg := range out.Segments {
		segs[i] = store.Segment{Start: sg.Start, End: sg.End, Text: sg.Text, Speaker: sg.Speaker}
	}
	t0 := time.Now()
	segs, err := s.findVoices(ctx, m, segs, right)
	if err != nil {
		s.log.Warn("meeting: voices", "memo", m.ID, "err", err)
		m.SpeakersStatus, m.SpeakersError = "failed", err.Error()
		return
	}
	out.Segments = make([]speech.Segment, len(segs))
	for i, sg := range segs {
		out.Segments[i] = speech.Segment{Start: sg.Start, End: sg.End, Text: sg.Text, Speaker: sg.Speaker}
	}
	out.Text = conversation(segs, func(k string) string { return speakerLabel(m, k) })
	s.log.Info("meeting: voices found", "memo", m.ID, "voices", len(m.Speakers), "seconds", int(time.Since(t0).Seconds()))
}

// ---- echo ----
//
// Without headphones the microphone hears the call too, so the person's
// channel repeats what the others said a moment earlier. A segment of the
// person's is an echo when most of its words were said by the others around
// the same time; echoes are dropped, so "You" is only what the person said.

const (
	echoWindowMS = 4000 // the others' words this close in time count
	echoShare    = 0.6  // this share of the words heard from the others: an echo
)

var wordRe = regexp.MustCompile(`[\p{L}\p{N}']+`)

func echoWords(t string) []string { return wordRe.FindAllString(strings.ToLower(t), -1) }

// dropEcho leaves out the person's segments that repeat the others.
func dropEcho(segs []speech.Segment) []speech.Segment {
	var others []speech.Segment
	for _, sg := range segs {
		if sg.Speaker != speakerYou {
			others = append(others, sg)
		}
	}
	if len(others) == 0 {
		return segs
	}
	out := segs[:0:0]
	for _, sg := range segs {
		if sg.Speaker == speakerYou && isEcho(sg, others) {
			continue
		}
		out = append(out, sg)
	}
	return out
}

func isEcho(sg speech.Segment, others []speech.Segment) bool {
	ws := echoWords(sg.Text)
	if len(ws) == 0 {
		return false
	}
	heard := map[string]bool{}
	for _, o := range others {
		if o.End > sg.Start-echoWindowMS && o.Start < sg.End+echoWindowMS {
			for _, w := range echoWords(o.Text) {
				heard[w] = true
			}
		}
	}
	n := 0
	for _, w := range ws {
		if heard[w] {
			n++
		}
	}
	return float64(n) >= echoShare*float64(len(ws))
}

// cleanEcho takes the echo out of meetings written out before it was caught,
// once; meetings whose words the person corrected are left alone.
func (s *Server) cleanEcho(ctx context.Context) {
	list, err := s.store.MeetingMemos(ctx)
	if err != nil {
		s.log.Warn("echo: list meetings", "err", err)
		return
	}
	cleaned := 0
	for _, x := range list {
		m, err := s.store.GetMemo(ctx, x.UserID, x.ID)
		if err != nil || m.Edited || m.Status != "done" {
			continue
		}
		segs := make([]speech.Segment, len(m.Segments))
		for i, sg := range m.Segments {
			segs[i] = speech.Segment{Start: sg.Start, End: sg.End, Text: sg.Text, Speaker: sg.Speaker}
		}
		kept := dropEcho(segs)
		if len(kept) == len(segs) {
			continue
		}
		kept, _ = mergeSpeakers(kept)
		m.Segments = make([]store.Segment, len(kept))
		for i, sg := range kept {
			m.Segments[i] = store.Segment{Start: sg.Start, End: sg.End, Text: sg.Text, Speaker: sg.Speaker}
		}
		m.Transcript = conversation(m.Segments, func(k string) string { return speakerLabel(m, k) })
		if err := s.store.SaveMemo(ctx, x.UserID, m); err != nil {
			s.log.Warn("echo: save", "memo", m.ID, "err", err)
			continue
		}
		s.log.Info("echo taken out of a meeting", "memo", m.ID, "segments", len(segs)-len(kept))
		cleaned++
	}
	if cleaned > 0 {
		s.log.Info("echo cleaned", "meetings", cleaned)
	}
}

// mergeSpeakers orders both speakers' segments by time and writes the
// conversation as lines "You: …" / "Others: …".
func mergeSpeakers(segs []speech.Segment) ([]speech.Segment, string) {
	sort.SliceStable(segs, func(i, j int) bool {
		if segs[i].Start != segs[j].Start {
			return segs[i].Start < segs[j].Start
		}
		return segs[i].Speaker == speakerYou && segs[j].Speaker != speakerYou // the person first
	})
	if segs == nil {
		segs = []speech.Segment{}
	}
	var lines []string
	cur, line := "", []string{}
	flush := func() {
		if len(line) > 0 {
			lines = append(lines, cur+": "+strings.Join(line, " "))
		}
	}
	for _, sg := range segs {
		t := strings.TrimSpace(sg.Text)
		if t == "" {
			continue
		}
		if sg.Speaker != cur {
			flush()
			cur, line = sg.Speaker, nil
		}
		line = append(line, t)
	}
	flush()
	return segs, strings.Join(lines, "\n")
}

const aiMeeting = `

This memo is a recorded meeting or call. Its lines start with who spoke: "You:" is the person; the other labels are the other people on the call, by name when known, else "Speaker 1", "Speaker 2", … (or "Others" for all of them).
- "text": the meeting as notes that are easy to read (the full words are kept elsewhere), covering the whole meeting from start to end:
  - split into its topics: before each topic a line "## " and the topic in a few words ("## Small talk", "## Arno: test costs and tickets"), and a blank line between topics;
  - one line per turn, starting with its label exactly as given ("You:", "Speaker 2:", a name); a speaker's lines that follow each other are one turn;
  - each turn condensed to what matters, in one to three sentences in the speaker's voice: facts, numbers, names, questions, what they will do. Turns that add nothing ("yeah", "okay", small talk) are left out or folded into one short line. Never merge different speakers, and never change who said what.
- "title": what the meeting was about.
- "summary": three to five sentences: what was discussed and how it ended.
- "decisions": what was decided, one short line each (empty when nothing was).
- "actions": the action items: tasks for the person (things they said they would do or were asked to do), with the day when one was said; appointments that were agreed.
Answer with JSON only: {"title":"","summary":"","decisions":[],"text":"","kind":"meeting","tags":[],"actions":[]}`
