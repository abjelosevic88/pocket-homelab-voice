package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
// silenced, and the two are merged by time into one conversation.

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
	channels, voiced, err := speech.SplitStereo(stereo, left, right)
	if err != nil || channels != 2 {
		// not stereo: one voice, written out as any memo
		return single()
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
		for _, sg := range res.Segments {
			sg.Speaker = p.speaker
			all = append(all, sg)
		}
		if out == nil || (out.Language == "" && res.Language != "") {
			lang := res.Language
			out = &speech.Result{Language: lang, Engine: res.Engine}
		}
	}
	out.Segments, out.Text = mergeSpeakers(all)
	return out, nil
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

This memo is a recorded meeting or call. Its lines start with who spoke: "You:" is the person, "Others:" the other people on the call.
- "text": the conversation cleaned up, keeping the "You:" / "Others:" labels at the start of each turn.
- "title": what the meeting was about.
- "summary": three to five sentences: what was discussed and how it ended.
- "decisions": what was decided, one short line each (empty when nothing was).
- "actions": the action items: tasks for the person (things they said they would do or were asked to do), with the day when one was said; appointments that were agreed.
Answer with JSON only: {"title":"","summary":"","decisions":[],"text":"","kind":"meeting","tags":[],"actions":[]}`
