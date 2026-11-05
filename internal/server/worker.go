package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/speech"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// ---- writing memos out ----
//
// One worker for the whole box takes the memo waiting longest, makes the
// 16 kHz WAV whisper reads (and an AAC copy every browser plays), writes it
// out with the person's own engine, language and words, then hands it to the
// AI to tidy up. One at a time: whisper uses all the cores it is given.

// progress is how far the memo being written out is.
type progress struct {
	Stage string `json:"stage"` // preparing | model | writing
	Pct   int    `json:"pct"`   // 0–100, -1 = unknown
}

func (s *Server) setProgress(id int64, stage string, pct int) {
	s.progMu.Lock()
	s.prog[id] = progress{stage, pct}
	s.progMu.Unlock()
}

func (s *Server) progressOf(id int64) (progress, bool) {
	s.progMu.Lock()
	defer s.progMu.Unlock()
	p, ok := s.prog[id]
	return p, ok
}

func (s *Server) clearProgress(id int64) {
	s.progMu.Lock()
	delete(s.prog, id)
	s.progMu.Unlock()
}

// enqueue wakes the worker.
func (s *Server) enqueue() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func (s *Server) worker(ctx context.Context) {
	if err := s.store.ResetWorking(ctx); err != nil {
		s.log.Warn("queue: reset", "err", err)
	}
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		for ctx.Err() == nil {
			m, err := s.store.NextQueued(ctx)
			if err != nil {
				s.log.Warn("queue: next", "err", err)
				break
			}
			if m == nil {
				break
			}
			s.process(ctx, m)
		}
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
		case <-tick.C:
		}
	}
}

// speechModel is the model this box writes with.
func (s *Server) speechModel(ctx context.Context) speech.Model {
	box, _ := s.store.GetSettings(ctx, 0)
	for _, id := range []string{box.SpeechModel, s.cfg.WhisperModel, speech.DefaultModel} {
		if m, ok := speech.ModelByID(id); ok && id != "" {
			return m
		}
	}
	m, _ := speech.ModelByID("")
	return m
}

// dictationModel is the model dictation from other apps is written out with (Parakeet unless an admin chose another).
func (s *Server) dictationModel(ctx context.Context) speech.Model {
	box, _ := s.store.GetSettings(ctx, 0)
	if m, ok := speech.ModelByID(box.DictationModel); ok && box.DictationModel != "" {
		return m
	}
	m, _ := speech.ModelByID(speech.ParakeetModel)
	return m
}

// noEngine is why this box can't run a model by itself.
func noEngine(m speech.Model) error {
	if m.Engine == "parakeet" {
		return errors.New("this server cannot run Parakeet (parakeet-cli is not installed here; the Docker image has it on Intel/AMD and ARM64). Pick a Whisper model under Settings → Writing out")
	}
	return errNoWhisper
}

func (s *Server) audioDir(uid int64) string {
	return filepath.Join(s.cfg.AudioDir, strconv.FormatInt(uid, 10))
}

func (s *Server) audioPath(uid int64, name string) string {
	return filepath.Join(s.audioDir(uid), filepath.Base(name))
}

func (s *Server) tmpDir() string {
	d := filepath.Join(s.cfg.DataDir, "tmp")
	_ = os.MkdirAll(d, 0o755)
	return d
}

// errNoWhisper is why a box without whisper.cpp cannot write out by itself.
var errNoWhisper = errors.New("this server cannot write out speech by itself (whisper.cpp is not installed here; the Docker image has it on Intel/AMD and ARM64). Choose \"OpenAI or a speech server\" under Settings → Writing out")

func (s *Server) process(ctx context.Context, m *store.Memo) {
	defer s.clearProgress(m.ID)
	s.setProgress(m.ID, "preparing", -1)
	st, _ := s.store.GetSettings(ctx, m.UserID)
	res, err := s.writeOut(ctx, m, st)
	if ctx.Err() != nil {
		return // shutting down: ResetWorking queues it again on start
	}
	if err != nil {
		m.Status, m.Error = store.StatusFailed, err.Error()
		if serr := s.store.SaveMemo(ctx, m.UserID, m); serr != nil {
			s.log.Warn("queue: save", "memo", m.ID, "err", serr)
		}
		s.log.Info("memo could not be written out", "memo", m.ID, "err", err)
		if st.NotifyFailed {
			s.pushMemo(ctx, m.UserID, st, m, "A memo could not be written out", firstNonEmpty(m.Title, "Your memo")+": "+err.Error())
		}
		return
	}
	s.applyResult(m, res, st)
	// the dictionary's replacements, in the words as they were heard
	if _, rp := s.dictionary(ctx, m.UserID); rp != nil && len(rp.rules) > 0 {
		uses := map[int64]int{}
		m.Transcript = rp.apply(m.Transcript, uses)
		for i := range m.Segments {
			m.Segments[i].Text = rp.apply(m.Segments[i].Text, nil)
		}
		s.store.CountWordUses(ctx, m.UserID, uses)
	}
	if err := s.store.SaveMemo(ctx, m.UserID, m); err != nil {
		s.log.Warn("queue: save", "memo", m.ID, "err", err)
		return
	}
	s.log.Info("memo written out", "memo", m.ID, "engine", m.Engine, "language", m.Language, "seconds", m.DurationMS/1000)
	s.publishMemo(ctx, m, "voice.memo_transcribed")
	long := m.DurationMS >= 3*60*1000 || m.Source == "upload" || m.Source == "import"
	if st.NotifyDone && long && m.Transcript != "" {
		s.pushMemo(ctx, m.UserID, st, m, "Written out: "+firstNonEmpty(m.Title, "your memo"), excerpt(m.Transcript, 180))
	}
	if m.Transcript != "" && st.AITidy {
		s.tidySoon(m.UserID, m.ID)
	}
}

// applyResult puts the words on the memo.
func (s *Server) applyResult(m *store.Memo, res *speech.Result, st store.Settings) {
	latin := st.Latin && latinLanguage(firstNonEmpty(res.Language, m.Language))
	m.Segments = make([]store.Segment, 0, len(res.Segments))
	for _, sg := range res.Segments {
		t := sg.Text
		if latin {
			t = speech.ToLatin(t)
		}
		m.Segments = append(m.Segments, store.Segment{Start: sg.Start, End: sg.End, Text: t, Speaker: sg.Speaker})
	}
	m.Transcript = res.Text
	if latin {
		m.Transcript = speech.ToLatin(m.Transcript)
	}
	if res.Language != "" {
		m.Language = res.Language
	}
	m.Engine, m.Status, m.Error, m.Edited = res.Engine, store.StatusDone, "", false
	if !m.TitleSet && (m.Title == "" || strings.HasPrefix(m.Title, "Memo ")) {
		m.Title = firstWords(m.Transcript, 7)
	}
}

// latinLanguage: the languages written in both alphabets, where most people
// type Latin.
func latinLanguage(code string) bool {
	switch code {
	case "", "sr", "bs", "hr", "sh", "cnr":
		return true
	}
	return false
}

// writeOut makes the WAV and the playable copy, then writes the words out.
func (s *Server) writeOut(ctx context.Context, m *store.Memo, st store.Settings) (*speech.Result, error) {
	if m.Audio == "" {
		return nil, errors.New("there is no recording to write out")
	}
	path := s.audioPath(m.UserID, m.Audio)
	if _, err := os.Stat(path); err != nil {
		return nil, errors.New("the recording file is missing")
	}
	wav := filepath.Join(s.tmpDir(), fmt.Sprintf("memo-%d-%d.wav", m.ID, time.Now().UnixNano()))
	defer os.Remove(wav)
	hasWAV := false
	if s.speech.HasFFmpeg() {
		if err := s.speech.ToWAV(ctx, path, wav); err != nil {
			return nil, err
		}
		hasWAV = true
		measured := false
		if dur, peaks, err := speech.WAVInfo(wav, 120); err == nil {
			m.DurationMS, m.Peaks, measured = dur, peaks, true
		}
		if !speech.Playable(m.AudioType) {
			name := strings.TrimSuffix(m.Audio, filepath.Ext(m.Audio)) + ".m4a"
			out := s.audioPath(m.UserID, name)
			conv := s.speech.ToM4A
			if m.Meeting {
				conv = s.speech.ToM4AStereo
			}
			if err := conv(ctx, path, out); err == nil {
				if out != path {
					_ = os.Remove(path)
				}
				path, m.Audio, m.AudioType = out, name, "audio/mp4"
				if fi, err := os.Stat(out); err == nil {
					m.AudioBytes = fi.Size()
				}
			} else {
				s.log.Warn("playable copy", "memo", m.ID, "err", err)
			}
		}
		if err := s.store.SaveMemo(ctx, m.UserID, m); err != nil {
			return nil, err
		}
		if measured && m.DurationMS < 600 {
			return &speech.Result{Engine: "too short", Segments: []speech.Segment{}}, nil
		}
	}
	lang := m.Language
	words, _ := s.dictionary(ctx, m.UserID)
	prompt := speechPrompt(st.Vocabulary, words)
	var run func(file string, progress func(int)) (*speech.Result, error)
	if st.Engine == "api" {
		s.setProgress(m.ID, "writing", -1)
		remote := speech.NewRemote(st.OpenAIBaseURL, st.OpenAIKey, st.SpeechAPI)
		run = func(file string, _ func(int)) (*speech.Result, error) {
			return remote.Transcribe(ctx, file, lang, prompt)
		}
		if !m.Meeting || !hasWAV {
			return run(path, nil)
		}
	} else {
		model := s.speechModel(ctx)
		if !s.speech.HasEngine(model) {
			return nil, noEngine(model)
		}
		if !hasWAV {
			if !strings.HasSuffix(strings.ToLower(path), ".wav") {
				return nil, errors.New("ffmpeg is not installed here, so only WAV recordings can be written out")
			}
			wav = path
		}
		if !s.speech.ModelReady(model) {
			if err := s.fetchModel(ctx, m.ID, model); err != nil {
				return nil, err
			}
		}
		s.setProgress(m.ID, "writing", 0)
		run = func(file string, progress func(int)) (*speech.Result, error) {
			return s.speech.Transcribe(ctx, file, model, lang, prompt, progress)
		}
	}
	if m.Meeting && hasWAV {
		return s.writeOutMeeting(ctx, m, path, wav, run)
	}
	return run(wav, func(p int) { s.setProgress(m.ID, "writing", p) })
}

// fetchModel downloads the speech model, showing its progress on the memo.
func (s *Server) fetchModel(ctx context.Context, memoID int64, model speech.Model) error {
	if s.cfg.DemoData {
		return errors.New("the speech model is not downloaded on a box that shows demo data")
	}
	s.setProgress(memoID, "model", 0)
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if d := s.speech.DownloadState(model.ID); d != nil && d.Total > 0 {
					s.setProgress(memoID, "model", int(d.Got*100/d.Total))
				}
			}
		}
	}()
	defer close(done)
	return s.speech.Fetch(ctx, model)
}

// vocabularyPrompt turns "Amra, Kibe Mahala, Pocket" into whisper's prompt.
func vocabularyPrompt(v string) string {
	v = strings.Join(strings.Fields(v), " ")
	if v == "" {
		return ""
	}
	if utf8.RuneCountInString(v) > 600 {
		v = string([]rune(v)[:600])
	}
	return v
}

// firstWords is a title from the first words of the text.
func firstWords(t string, n int) string {
	t = strings.TrimSpace(strings.SplitN(t, "\n", 2)[0])
	if i := strings.IndexAny(t, ".!?"); i > 10 {
		t = t[:i]
	}
	w := strings.Fields(t)
	more := len(w) > n
	if more {
		w = w[:n]
	}
	out := strings.TrimRight(strings.Join(w, " "), ",;:—-")
	if more {
		out += "…"
	}
	if out != "" {
		r := []rune(out)
		r[0] = []rune(strings.ToUpper(string(r[0])))[0]
		out = string(r)
	}
	return out
}

func excerpt(t string, n int) string {
	t = strings.Join(strings.Fields(t), " ")
	if utf8.RuneCountInString(t) <= n {
		return t
	}
	return strings.TrimSpace(string([]rune(t)[:n])) + "…"
}

// tidyFiles lets go of recordings older than each person keeps them (the
// words stay) and empties the scratch folder.
func (s *Server) tidyFiles(ctx context.Context) {
	if entries, err := os.ReadDir(s.tmpDir()); err == nil {
		for _, e := range entries {
			if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > 6*time.Hour {
				_ = os.Remove(filepath.Join(s.tmpDir(), e.Name()))
			}
		}
	}
	ids, err := s.store.SettingsUsers(ctx)
	if err != nil {
		return
	}
	for _, uid := range ids {
		st, err := s.store.GetSettings(ctx, uid)
		if err != nil || st.KeepAudio <= 0 {
			continue
		}
		old, err := s.store.AudioOlderThan(ctx, uid, time.Now().AddDate(0, 0, -st.KeepAudio))
		if err != nil {
			continue
		}
		for i := range old {
			m := &old[i]
			if m.Starred {
				continue // a starred memo keeps its recording
			}
			_ = os.Remove(s.audioPath(uid, m.Audio))
			m.Audio, m.AudioType, m.AudioBytes, m.AudioGoneAt = "", "", 0, store.Now()
			_ = s.store.SaveMemo(ctx, uid, m)
		}
		if len(old) > 0 {
			s.log.Info("recordings let go", "user", uid, "count", len(old), "older_than_days", st.KeepAudio)
		}
	}
}
