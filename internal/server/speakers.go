package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/speakers"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/speech"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// ---- who spoke: the voices of a meeting, with names ----
//
// The call side of a meeting holds everyone else. After it is written out,
// the speaker finder splits it into its voices (S1, S2, … in the order they
// first spoke); each voice is compared with the people the person has named
// before. A voice much like someone's is named after them ("auto"), one a bit
// like someone is asked about ("Is this Alex?"), the rest wait for a name.
// Every name the person gives or confirms teaches the voice: the next meeting
// knows it. Voice prints stay on this box.

const (
	autoScore    = 0.72 // this much like a person's voice: it is them
	suggestScore = 0.50 // this much: ask whether it is them
	learnAuto    = 0.80 // a recognition this sure also teaches the voice
	sampleMaxMS  = 8000 // the stretch to listen to
)

// speakerNum is the number in a key: "S3" → 3.
func speakerNum(key string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(key, "S"))
	return n
}

// speakerLabel is how a speaker is written in the conversation.
func speakerLabel(m *store.Memo, key string) string {
	if key == speakerYou {
		return speakerYou
	}
	for _, sp := range m.Speakers {
		if sp.Key == key {
			if sp.Name != "" {
				return sp.Name
			}
			break
		}
	}
	if n := speakerNum(key); n > 0 {
		return fmt.Sprintf("Speaker %d", n)
	}
	if key == "" {
		return speakerOthers
	}
	return key
}

func speakerOf(m *store.Memo, key string) *store.Speaker {
	for i := range m.Speakers {
		if m.Speakers[i].Key == key {
			return &m.Speakers[i]
		}
	}
	return nil
}

// conversation writes segments as "Label: …" lines, one per turn.
func conversation(segs []store.Segment, label func(string) string) string {
	var lines []string
	cur, line := "\x00", []string{}
	flush := func() {
		if len(line) > 0 {
			lines = append(lines, label(cur)+": "+strings.Join(line, " "))
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
	return strings.Join(lines, "\n")
}

// rebuildTranscript writes a meeting's words again with its speakers' names
// (unless the person corrected the words by hand).
func rebuildTranscript(m *store.Memo) {
	if m.Edited || !hasSpeakerKeys(m.Segments) {
		return
	}
	m.Transcript = conversation(m.Segments, func(k string) string { return speakerLabel(m, k) })
}

func hasSpeakerKeys(segs []store.Segment) bool {
	for _, sg := range segs {
		if sg.Speaker != "" {
			return true
		}
	}
	return false
}

// relabel changes a speaker's label in text the AI wrote: "Speaker 3:" at
// the start of a line and "Speaker 3" as a word.
func relabel(text, from, to string) string {
	if text == "" || from == "" || from == to {
		return text
	}
	re := regexp.MustCompile(`(^|[^\p{L}\p{N}])` + regexp.QuoteMeta(from) + `($|[^\p{L}\p{N}])`)
	for i := 0; i < 3; i++ { // overlapping matches ("Speaker 3 and Speaker 3")
		text = re.ReplaceAllString(text, "${1}"+strings.ReplaceAll(to, "$", "$$")+"${2}")
	}
	return text
}

// ---- finding the voices ----

var normRe = regexp.MustCompile(`[^\p{L}\p{N}]+`)

func normText(t string) string {
	return strings.TrimSpace(normRe.ReplaceAllString(strings.ToLower(t), " "))
}

// identify splits the call side of a meeting's segments into the voices the
// speaker finder heard and names them. It sets m.Speakers and m.SpeakerVoices
// and returns the segments with their speaker keys.
func identify(m *store.Memo, segs []store.Segment, res *speakers.Result) []store.Segment {
	turns := res.Turns
	// the speaker finder's numbers → S1, S2, … in the order they first spoke
	order := map[int]string{}
	for _, t := range turns {
		if _, ok := order[t.K]; !ok {
			order[t.K] = fmt.Sprintf("S%d", len(order)+1)
		}
	}
	callSide := func(sg store.Segment) bool { return sg.Speaker != speakerYou }

	// how often each line the speaker finder heard nobody in was said: speech
	// models repeat a phrase over silence ("Let's go." ×15)
	orphanCount := map[string]int{}
	overlap := func(sg store.Segment) map[int]int64 {
		ov := map[int]int64{}
		for _, t := range turns {
			if o := min(t.E, sg.End) - max(t.S, sg.Start); o > 0 {
				ov[t.K] += o
			}
		}
		return ov
	}
	for _, sg := range segs {
		if callSide(sg) && len(overlap(sg)) == 0 {
			orphanCount[normText(sg.Text)]++
		}
	}
	out := make([]store.Segment, 0, len(segs))
	var prevText string
	repeat := 0
	for _, sg := range segs {
		if !callSide(sg) {
			out = append(out, sg)
			continue
		}
		// the same words over and over in a row: the model looping
		if nt := normText(sg.Text); nt != "" && nt == prevText {
			repeat++
			if repeat >= 2 {
				continue
			}
		} else {
			prevText, repeat = nt, 0
		}
		ov := overlap(sg)
		if len(ov) == 0 {
			if orphanCount[normText(sg.Text)] >= 3 {
				continue // said over silence again and again: not said at all
			}
			// nobody heard right there: the nearest voice in time
			best, dist := -1, int64(math.MaxInt64)
			for _, t := range turns {
				d := max(t.S-sg.End, sg.Start-t.E, 0)
				if d < dist {
					best, dist = t.K, d
				}
			}
			if best < 0 {
				out = append(out, sg)
				continue
			}
			ov[best] = 1
		}
		k, most := -1, int64(-1)
		for kk, v := range ov {
			if v > most || (v == most && kk < k) {
				k, most = kk, v
			}
		}
		sg.Speaker = order[k]
		out = append(out, sg)
	}

	// the speakers: you, then the voices that have words, in order
	said := map[string]int64{}
	for _, sg := range out {
		said[sg.Speaker] += sg.End - sg.Start
	}
	var list []store.Speaker
	if said[speakerYou] > 0 {
		list = append(list, store.Speaker{Key: speakerYou, Name: speakerYou, State: store.SpeakerYou, MS: said[speakerYou]})
	}
	voices := map[string][]float32{}
	byKey := map[string]speakers.Voice{}
	for _, v := range res.Speakers {
		byKey[order[v.K]] = v
	}
	keys := make([]string, 0, len(order))
	for _, k := range order {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return speakerNum(keys[i]) < speakerNum(keys[j]) })
	for _, key := range keys {
		if said[key] == 0 {
			continue
		}
		sp := store.Speaker{Key: key, State: store.SpeakerUnknown}
		var longest speakers.Turn
		for _, t := range turns {
			if order[t.K] != key {
				continue
			}
			sp.MS += t.E - t.S
			if t.E-t.S > longest.E-longest.S {
				longest = t
			}
		}
		sp.Sample = [2]int64{longest.S, min(longest.E, longest.S+sampleMaxMS)}
		if v, ok := byKey[key]; ok && len(v.Emb) > 0 {
			voices[key] = v.Emb
		}
		list = append(list, sp)
	}
	m.Speakers, m.SpeakerVoices = list, voices
	return out
}

// personVoice is a person's voice: their prints weighted by how much was heard
// (a sure recognition counts half of a name the person gave).
func personVoice(p *store.Person) []float32 {
	var embs [][]float32
	var w []float64
	for _, v := range p.Voices {
		x := math.Sqrt(float64(max(v.MS, 1000)) / 1000)
		if v.Auto {
			x /= 2
		}
		embs = append(embs, v.Emb)
		w = append(w, x)
	}
	return speakers.Mean(embs, w)
}

// matchPeople names the voices of a meeting that have no name from the
// person yet: surely someone → their name; maybe someone → a question.
// It returns the people recognised surely enough to learn from.
func matchPeople(m *store.Memo, people []store.Person) []int64 {
	type pair struct {
		key   string
		pid   int64
		score float64
	}
	taken := map[int64]bool{}
	voices := map[int64][]float32{}
	names := map[int64]string{}
	for i := range people {
		voices[people[i].ID] = personVoice(&people[i])
		names[people[i].ID] = people[i].Name
	}
	var pairs []pair
	for i := range m.Speakers {
		sp := &m.Speakers[i]
		if sp.State == store.SpeakerYou {
			continue
		}
		if sp.State == store.SpeakerConfirmed {
			taken[sp.PersonID] = true
			continue
		}
		// a fresh look: whatever was guessed before is guessed again
		sp.Name, sp.PersonID, sp.Score, sp.Suggest, sp.SuggestID, sp.State = "", 0, 0, "", 0, store.SpeakerUnknown
		emb := m.SpeakerVoices[sp.Key]
		if len(emb) == 0 {
			continue
		}
		for pid, v := range voices {
			if len(v) == 0 || containsID(sp.Rejected, pid) {
				continue
			}
			if sc := speakers.Similarity(emb, v); sc >= suggestScore {
				pairs = append(pairs, pair{sp.Key, pid, sc})
			}
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].score > pairs[j].score })
	done := map[string]bool{}
	var learn []int64
	for _, p := range pairs {
		if done[p.key] || taken[p.pid] {
			continue
		}
		sp := speakerOf(m, p.key)
		done[p.key], taken[p.pid] = true, true
		sp.Score = math.Round(p.score*100) / 100
		if p.score >= autoScore {
			sp.State, sp.Name, sp.PersonID = store.SpeakerAuto, names[p.pid], p.pid
			if p.score >= learnAuto {
				learn = append(learn, p.pid)
			}
		} else {
			sp.State, sp.Suggest, sp.SuggestID = store.SpeakerSuggested, names[p.pid], p.pid
		}
	}
	return learn
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// learnVoice keeps this meeting's print of a speaker in the person's voice
// (one print per meeting and person).
func (s *Server) learnVoice(ctx context.Context, uid int64, m *store.Memo, key string, pid int64, auto bool) {
	emb := m.SpeakerVoices[key]
	sp := speakerOf(m, key)
	if len(emb) == 0 || sp == nil || pid == 0 {
		return
	}
	p, err := s.store.GetPerson(ctx, uid, pid)
	if err != nil {
		return
	}
	kept := p.Voices[:0]
	for _, v := range p.Voices {
		if v.MemoID != m.ID {
			kept = append(kept, v)
		}
	}
	p.Voices = append(kept, store.Voice{Emb: emb, MS: sp.MS, MemoID: m.ID, At: store.Now(), Auto: auto})
	if err := s.store.SavePerson(ctx, uid, p); err != nil {
		s.log.Warn("speakers: learn", "memo", m.ID, "person", pid, "err", err)
	}
}

// unlearnVoice takes this meeting's print out of a person's voice.
func (s *Server) unlearnVoice(ctx context.Context, uid int64, memoID, pid int64) {
	p, err := s.store.GetPerson(ctx, uid, pid)
	if err != nil {
		return
	}
	kept := p.Voices[:0]
	for _, v := range p.Voices {
		if v.MemoID != memoID {
			kept = append(kept, v)
		}
	}
	if len(kept) != len(p.Voices) {
		p.Voices = kept
		_ = s.store.SavePerson(ctx, uid, p)
	}
}

// nameVoices matches a meeting's voices with the person's people and learns
// from the sure ones.
func (s *Server) nameVoices(ctx context.Context, uid int64, m *store.Memo) {
	people, err := s.store.ListPeople(ctx, uid)
	if err != nil {
		return
	}
	for _, pid := range matchPeople(m, people) {
		for _, sp := range m.Speakers {
			if sp.PersonID == pid && sp.State == store.SpeakerAuto {
				s.learnVoice(ctx, uid, m, sp.Key, pid, true)
			}
		}
	}
}

// minSpeakerS: a voice with less speech than this is someone else's (the
// speaker finder folds it into the nearest); shorter meetings allow less.
func minSpeakerS(durMS int64) float64 {
	return math.Max(4, math.Min(20, float64(durMS)/1000/60*0.6))
}

// findVoices runs the speaker finder on the call side of a meeting and puts
// the voices on m (not saved). others is the call side as a 16 kHz mono WAV.
func (s *Server) findVoices(ctx context.Context, m *store.Memo, segs []store.Segment, others string) ([]store.Segment, error) {
	res, err := s.speakers.Diarize(ctx, others, speakers.Options{Background: true, MinSpeakerS: minSpeakerS(m.DurationMS)})
	if err != nil {
		return nil, err
	}
	out := identify(m, segs, res)
	s.nameVoices(ctx, m.UserID, m)
	m.SpeakersStatus, m.SpeakersError = "done", ""
	return out, nil
}

// speakersAvailable reports whether this box can find who spoke.
func (s *Server) speakersAvailable() (bool, string) {
	if s.cfg.DemoData {
		return false, "not on a box that shows demo data"
	}
	return s.speakers.Available()
}

// ---- finding the voices of a meeting written out before ----

// findVoicesLater finds who spoke in a meeting already written out: its
// recording is split again and the call side goes to the speaker finder.
func (s *Server) findVoicesLater(uid, id int64) {
	go func() {
		ctx, cancel := context.WithTimeout(s.bg, 2*time.Hour)
		defer cancel()
		select {
		case s.spkSem <- struct{}{}:
			defer func() { <-s.spkSem }()
		case <-ctx.Done():
			return
		}
		err := s.findVoicesNow(ctx, uid, id)
		if err != nil {
			s.log.Info("speakers: could not find who spoke", "memo", id, "err", err)
			if m, gerr := s.store.GetMemo(ctx, uid, id); gerr == nil {
				m.SpeakersStatus, m.SpeakersError = "failed", err.Error()
				_ = s.store.SaveMemo(ctx, uid, m)
			}
		}
	}()
}

func (s *Server) findVoicesNow(ctx context.Context, uid, id int64) error {
	m, err := s.store.GetMemo(ctx, uid, id)
	if err != nil {
		return err
	}
	path, fetched, err := s.localAudio(ctx, m)
	if err != nil {
		return err
	}
	defer fetched()
	base := filepath.Join(s.tmpDir(), fmt.Sprintf("voices-%d-%d", m.ID, time.Now().UnixNano()))
	stereo, left, right := base+"-stereo.wav", base+"-you.wav", base+"-others.wav"
	defer os.Remove(stereo)
	defer os.Remove(left)
	defer os.Remove(right)
	bg := speech.Background(ctx)
	if err := s.speech.ToWAVChannels(bg, path, stereo); err != nil {
		return err
	}
	channels, _, _, err := speech.SplitStereo(stereo, left, right)
	if err != nil {
		return err
	}
	if channels != 2 {
		return errors.New("this recording has one channel, so the call cannot be told apart from you")
	}
	t0 := time.Now()
	res, err := s.speakers.Diarize(ctx, right, speakers.Options{Background: true, MinSpeakerS: minSpeakerS(m.DurationMS)})
	if err != nil {
		return err
	}
	// read it again: the person may have changed it meanwhile
	cur, err := s.store.GetMemo(ctx, uid, id)
	if err != nil {
		return err
	}
	prev := cur.Speakers
	cur.Segments = identify(cur, cur.Segments, res)
	keepNames(cur, prev)
	s.nameVoices(ctx, uid, cur)
	cur.SpeakersStatus, cur.SpeakersError = "done", ""
	before := cur.Transcript
	rebuildTranscript(cur)
	relabelOthers(cur)
	if err := s.store.SaveMemo(ctx, uid, cur); err != nil {
		return err
	}
	s.log.Info("speakers found", "memo", id, "voices", len(cur.Speakers), "seconds", int(time.Since(t0).Seconds()), "transcript_changed", before != cur.Transcript)
	s.hintsSoon(uid, id)
	s.retidyIfOld(ctx, cur)
	return nil
}

// oldTidy: the AI's tidy-up was written before the voices were told apart.
func oldTidy(m *store.Memo) bool {
	return len(m.Speakers) > 0 && (strings.HasPrefix(m.Tidy, speakerOthers+":") || strings.Contains(m.Tidy, "\n"+speakerOthers+":"))
}

// retidyIfOld writes a meeting's tidy-up again with its voices (when the
// person has the AI tidy up).
func (s *Server) retidyIfOld(ctx context.Context, m *store.Memo) {
	if !oldTidy(m) {
		return
	}
	if st, err := s.store.GetSettings(ctx, m.UserID); err == nil && st.AITidy {
		s.log.Info("meeting: tidying up again with its voices", "memo", m.ID)
		s.tidySoon(m.UserID, m.ID)
	}
}

// retidyOld does that for every meeting whose tidy-up is from before.
func (s *Server) retidyOld(ctx context.Context) {
	list, err := s.store.MeetingsWithOldTidy(ctx)
	if err != nil {
		return
	}
	for _, x := range list {
		if m, err := s.store.GetMemo(ctx, x.UserID, x.ID); err == nil {
			s.retidyIfOld(ctx, m)
		}
	}
}

// keepNames carries the names the person gave over to a new split of the
// same meeting, by voice.
func keepNames(m *store.Memo, prev []store.Speaker) {
	for _, old := range prev {
		if old.State != store.SpeakerConfirmed || old.PersonID == 0 {
			continue
		}
		// the new voice most like the old one, by where they spoke
		for i := range m.Speakers {
			sp := &m.Speakers[i]
			if sp.State == store.SpeakerUnknown && sp.Sample[0] <= old.Sample[1] && old.Sample[0] <= sp.Sample[1] {
				sp.Name, sp.PersonID, sp.State = old.Name, old.PersonID, store.SpeakerConfirmed
				break
			}
		}
	}
}

// relabelOthers: the AI's tidy text of a meeting written before its voices
// were found says "Others:"; with one other voice, that is them.
func relabelOthers(m *store.Memo) {
	var others []store.Speaker
	for _, sp := range m.Speakers {
		if sp.State != store.SpeakerYou {
			others = append(others, sp)
		}
	}
	if len(others) == 1 {
		l := speakerLabel(m, others[0].Key)
		m.Tidy = relabel(m.Tidy, speakerOthers, l)
		m.Summary = relabel(m.Summary, speakerOthers, l)
	}
}

// findMissingVoices looks for the voices of the meetings written out before
// this server could tell them apart: once each, one at a time, gently.
func (s *Server) findMissingVoices(ctx context.Context) {
	if ok, _ := s.speakersAvailable(); !ok {
		return
	}
	list, err := s.store.MeetingsWithoutVoices(ctx)
	if err != nil || len(list) == 0 {
		return
	}
	s.log.Info("speakers: meetings to tell the voices apart in", "count", len(list))
	for _, x := range list {
		m, err := s.store.GetMemo(ctx, x.UserID, x.ID)
		if err != nil {
			continue
		}
		m.SpeakersStatus = "working"
		if err := s.store.SaveMemo(ctx, x.UserID, m); err == nil {
			s.findVoicesLater(x.UserID, x.ID)
		}
	}
}

// ---- the person names the voices ----

func (s *Server) handleFindSpeakers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	m, err := s.store.GetMemo(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "memo")
		return
	}
	switch {
	case !m.Meeting:
		writeErr(w, http.StatusBadRequest, "only a meeting (a call recorded in stereo) can be split into its voices")
		return
	case !m.HasAudio:
		writeErr(w, http.StatusBadRequest, "the recording is not kept anymore")
		return
	case m.Status != store.StatusDone:
		writeErr(w, http.StatusConflict, "the meeting is not written out yet")
		return
	case m.SpeakersStatus == "working":
		writeJSON(w, http.StatusAccepted, s.memoOut(ctx, m))
		return
	}
	if ok, why := s.speakersAvailable(); !ok {
		writeErr(w, http.StatusConflict, "this server cannot find who spoke: "+why)
		return
	}
	m.SpeakersStatus, m.SpeakersError = "working", ""
	if err := s.store.SaveMemo(ctx, uid, m); err != nil {
		s.fail(w, err, "memo")
		return
	}
	s.findVoicesLater(uid, m.ID)
	writeJSON(w, http.StatusAccepted, s.memoOut(ctx, m))
}

// handleNameSpeaker: {"name": "Alex"} or {"person_id": 3} names a voice (and
// teaches it), {"name": ""} takes the name off, {"answer": "yes"|"no"}
// answers "Is this Alex?" or a recognition.
func (s *Server) handleNameSpeaker(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	m, err := s.store.GetMemo(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "memo")
		return
	}
	key := chiParam(r, "key")
	sp := speakerOf(m, key)
	if sp == nil || sp.State == store.SpeakerYou {
		writeErr(w, http.StatusNotFound, "no such speaker")
		return
	}
	var in struct {
		Name     *string `json:"name"`
		PersonID int64   `json:"person_id"`
		Answer   string  `json:"answer"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	oldLabel := speakerLabel(m, key)
	var pid int64
	switch {
	case in.Answer == "yes":
		pid = firstID(sp.PersonID, sp.SuggestID)
		if pid == 0 {
			writeErr(w, http.StatusBadRequest, "there is nobody to say yes to")
			return
		}
	case in.Answer == "no":
		bad := firstID(sp.PersonID, sp.SuggestID)
		if bad != 0 {
			sp.Rejected = append(sp.Rejected, bad)
			if sp.State == store.SpeakerAuto {
				s.unlearnVoice(ctx, uid, m.ID, bad)
			}
		}
		sp.State = store.SpeakerUnknown
		s.nameVoices(ctx, uid, m) // maybe someone else
		s.saveNamed(ctx, w, m, key, oldLabel)
		return
	case in.PersonID != 0:
		p, err := s.store.GetPerson(ctx, uid, in.PersonID)
		if err != nil {
			s.fail(w, err, "person")
			return
		}
		pid = p.ID
	case in.Name != nil:
		name := strings.Join(strings.Fields(*in.Name), " ")
		if name == "" {
			if sp.State == store.SpeakerConfirmed && sp.PersonID != 0 {
				s.unlearnVoice(ctx, uid, m.ID, sp.PersonID)
			}
			sp.Name, sp.PersonID, sp.State, sp.Suggest, sp.SuggestID, sp.Score = "", 0, store.SpeakerUnknown, "", 0, 0
			s.saveNamed(ctx, w, m, key, oldLabel)
			return
		}
		if len([]rune(name)) > 60 || strings.EqualFold(name, speakerYou) {
			writeErr(w, http.StatusBadRequest, "a name is 1 to 60 characters, and not \"You\"")
			return
		}
		p, err := s.store.PersonByName(ctx, uid, name)
		if errors.Is(err, store.ErrNotFound) {
			p = &store.Person{Name: name}
			err = s.store.CreatePerson(ctx, uid, p)
		}
		if err != nil {
			s.fail(w, err, "person")
			return
		}
		pid = p.ID
	default:
		writeErr(w, http.StatusBadRequest, "send a name, a person_id or an answer")
		return
	}
	p, err := s.store.GetPerson(ctx, uid, pid)
	if err != nil {
		s.fail(w, err, "person")
		return
	}
	if sp.PersonID != 0 && sp.PersonID != pid && sp.State == store.SpeakerConfirmed {
		s.unlearnVoice(ctx, uid, m.ID, sp.PersonID)
	}
	// the same person under two voices: one speaker
	if other := speakerOfPerson(m, pid, key); other != nil && other.State == store.SpeakerConfirmed {
		key = mergeSpeaker(m, key, other.Key)
		sp = speakerOf(m, key)
	}
	sp.Name, sp.PersonID, sp.State, sp.Suggest, sp.SuggestID = p.Name, p.ID, store.SpeakerConfirmed, "", 0
	sp.Rejected = removeID(sp.Rejected, pid)
	s.learnVoice(ctx, uid, m, key, pid, false)
	// someone else in this meeting was guessed to be them: guess again
	for i := range m.Speakers {
		o := &m.Speakers[i]
		if o.Key != key && (o.PersonID == pid || o.SuggestID == pid) && o.State != store.SpeakerConfirmed {
			o.State = store.SpeakerUnknown
		}
	}
	s.nameVoices(ctx, uid, m)
	s.saveNamed(ctx, w, m, key, oldLabel)
}

func firstID(ids ...int64) int64 {
	for _, x := range ids {
		if x != 0 {
			return x
		}
	}
	return 0
}

func removeID(ids []int64, id int64) []int64 {
	out := ids[:0]
	for _, x := range ids {
		if x != id {
			out = append(out, x)
		}
	}
	return out
}

func speakerOfPerson(m *store.Memo, pid int64, except string) *store.Speaker {
	for i := range m.Speakers {
		if m.Speakers[i].Key != except && m.Speakers[i].PersonID == pid {
			return &m.Speakers[i]
		}
	}
	return nil
}

// mergeSpeaker puts speaker from's words under speaker into and drops from;
// it returns the key that stays.
func mergeSpeaker(m *store.Memo, from, into string) string {
	a, b := speakerOf(m, from), speakerOf(m, into)
	if a == nil || b == nil || from == into {
		return into
	}
	for i := range m.Segments {
		if m.Segments[i].Speaker == from {
			m.Segments[i].Speaker = into
		}
	}
	if va, vb := m.SpeakerVoices[from], m.SpeakerVoices[into]; len(va) > 0 && len(vb) > 0 {
		m.SpeakerVoices[into] = speakers.Mean([][]float32{va, vb}, []float64{float64(a.MS), float64(b.MS)})
	}
	if a.MS > b.MS {
		b.Sample = a.Sample
	}
	b.MS += a.MS
	delete(m.SpeakerVoices, from)
	list := m.Speakers[:0]
	for _, sp := range m.Speakers {
		if sp.Key != from {
			list = append(list, sp)
		}
	}
	m.Speakers = list
	return into
}

// saveNamed writes the names into the words and saves the memo.
func (s *Server) saveNamed(ctx context.Context, w http.ResponseWriter, m *store.Memo, key, oldLabel string) {
	if nl := speakerLabel(m, key); nl != oldLabel {
		m.Tidy = relabel(m.Tidy, oldLabel, nl)
		m.Summary = relabel(m.Summary, oldLabel, nl)
	}
	rebuildTranscript(m)
	if err := s.store.SaveMemo(ctx, m.UserID, m); err != nil {
		s.fail(w, err, "memo")
		return
	}
	writeJSON(w, http.StatusOK, s.memoOut(ctx, m))
}

// handleMoveSegments: {"indexes": [4, 5], "speaker": "S2"} says who really
// said these words; "speaker": "" gives them to a new voice.
func (s *Server) handleMoveSegments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	m, err := s.store.GetMemo(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "memo")
		return
	}
	var in struct {
		Indexes []int  `json:"indexes"`
		Speaker string `json:"speaker"`
	}
	if err := decode(r, &in); err != nil || len(in.Indexes) == 0 {
		writeErr(w, http.StatusBadRequest, "send indexes and a speaker")
		return
	}
	for _, i := range in.Indexes {
		if i < 0 || i >= len(m.Segments) {
			writeErr(w, http.StatusBadRequest, "no such segment")
			return
		}
	}
	key := in.Speaker
	if key == "" {
		n := 0
		for _, sp := range m.Speakers {
			n = max(n, speakerNum(sp.Key))
		}
		key = fmt.Sprintf("S%d", n+1)
		m.Speakers = append(m.Speakers, store.Speaker{Key: key, State: store.SpeakerUnknown})
	} else if speakerOf(m, key) == nil {
		writeErr(w, http.StatusBadRequest, "no such speaker")
		return
	}
	for _, i := range in.Indexes {
		m.Segments[i].Speaker = key
	}
	// talk time follows the words; a voice left with none goes
	said := map[string]int64{}
	for _, sg := range m.Segments {
		said[sg.Speaker] += sg.End - sg.Start
	}
	list := m.Speakers[:0]
	for _, sp := range m.Speakers {
		if said[sp.Key] > 0 {
			if sp.MS == 0 || sp.Key == key {
				sp.MS = said[sp.Key]
			}
			if sp.Key == key && sp.Sample == [2]int64{} {
				for _, i := range in.Indexes {
					sp.Sample = [2]int64{m.Segments[i].Start, min(m.Segments[i].End, m.Segments[i].Start+sampleMaxMS)}
					break
				}
			}
			list = append(list, sp)
		}
	}
	m.Speakers = list
	rebuildTranscript(m)
	if err := s.store.SaveMemo(ctx, uid, m); err != nil {
		s.fail(w, err, "memo")
		return
	}
	writeJSON(w, http.StatusOK, s.memoOut(ctx, m))
}

// ---- people ----

type personView struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	Voices   int     `json:"voices"`   // voice prints learned
	Meetings int     `json:"meetings"` // meetings they were named or recognised in
	HeardMS  int64   `json:"heard_ms"`
	LastAt   string  `json:"last_at"`
	Learned  float64 `json:"learned"` // 0–1: how well the voice is known
}

func viewPerson(p *store.Person) personView {
	v := personView{ID: p.ID, Name: p.Name, Voices: len(p.Voices)}
	memos := map[int64]bool{}
	for _, x := range p.Voices {
		memos[x.MemoID] = true
		v.HeardMS += x.MS
		if x.At > v.LastAt {
			v.LastAt = x.At
		}
	}
	v.Meetings = len(memos)
	// a minute heard in three meetings is a voice well known
	v.Learned = math.Round((math.Min(1, float64(v.HeardMS)/180000)*0.5+math.Min(1, float64(v.Meetings)/3)*0.5)*100) / 100
	return v
}

func (s *Server) handleListPeople(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	people, err := s.store.ListPeople(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "people")
		return
	}
	out := make([]personView, 0, len(people))
	for i := range people {
		out = append(out, viewPerson(&people[i]))
	}
	ok, why := s.speakersAvailable()
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "available": ok, "detail": why})
}

// forEachMemoWith changes every memo that names a person.
func (s *Server) forEachMemoWith(ctx context.Context, uid, pid int64, change func(m *store.Memo)) {
	ids, err := s.store.MemosWithPerson(ctx, uid, pid)
	if err != nil {
		s.log.Warn("people: memos", "person", pid, "err", err)
		return
	}
	for _, id := range ids {
		m, err := s.store.GetMemo(ctx, uid, id)
		if err != nil {
			continue
		}
		change(m)
		rebuildTranscript(m)
		_ = s.store.SaveMemo(ctx, uid, m)
	}
}

func (s *Server) handleUpdatePerson(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	p, err := s.store.GetPerson(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "person")
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	name := strings.Join(strings.Fields(in.Name), " ")
	if name == "" || len([]rune(name)) > 60 || strings.EqualFold(name, speakerYou) {
		writeErr(w, http.StatusBadRequest, "a name is 1 to 60 characters, and not \"You\"")
		return
	}
	old := p.Name
	p.Name = name
	if err := s.store.SavePerson(ctx, uid, p); err != nil {
		s.fail(w, err, "person")
		return
	}
	if old != name {
		s.forEachMemoWith(ctx, uid, p.ID, func(m *store.Memo) {
			for i := range m.Speakers {
				sp := &m.Speakers[i]
				if sp.PersonID == p.ID {
					sp.Name = name
				}
				if sp.SuggestID == p.ID {
					sp.Suggest = name
				}
			}
			m.Tidy, m.Summary = relabel(m.Tidy, old, name), relabel(m.Summary, old, name)
		})
	}
	writeJSON(w, http.StatusOK, viewPerson(p))
}

// handleDeletePerson forgets someone: their voice prints go, and the memos
// that named them call those voices "Speaker n" again.
func (s *Server) handleDeletePerson(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	p, err := s.store.GetPerson(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "person")
		return
	}
	s.forEachMemoWith(ctx, uid, p.ID, func(m *store.Memo) {
		for i := range m.Speakers {
			sp := &m.Speakers[i]
			if sp.PersonID == p.ID {
				m.Tidy = relabel(m.Tidy, p.Name, fmt.Sprintf("Speaker %d", speakerNum(sp.Key)))
				m.Summary = relabel(m.Summary, p.Name, fmt.Sprintf("Speaker %d", speakerNum(sp.Key)))
				sp.Name, sp.PersonID, sp.State, sp.Score = "", 0, store.SpeakerUnknown, 0
			}
			if sp.SuggestID == p.ID {
				sp.Suggest, sp.SuggestID = "", 0
				if sp.State == store.SpeakerSuggested {
					sp.State = store.SpeakerUnknown
				}
			}
			sp.Rejected = removeID(sp.Rejected, p.ID)
		}
	})
	if err := s.store.DeletePerson(ctx, uid, p.ID); err != nil {
		s.fail(w, err, "person")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
