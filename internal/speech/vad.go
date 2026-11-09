package speech

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
)

// The Silero voice activity model (v5.1.2, MIT licence, 885 KB) in whisper.cpp's
// format, from huggingface.co/ggml-org/whisper-vad. Whisper given silence makes
// words up, and once it is looping it can lose the rest of a long recording
// (a meeting's silent microphone side came back as "And they're going to help
// you." 1,175 times while the call side lost 30 minutes); with --vad it only
// hears the stretches where someone speaks.
//
//go:embed vad/ggml-silero-v5.1.2.bin
var sileroModel []byte

const sileroFile = "ggml-silero-v5.1.2.bin"

// vadModel is the path of the voice activity model, written next to the speech
// models the first time ("" when it cannot be).
func (l *Local) vadModel() string {
	if l.Dir == "" {
		return ""
	}
	p := filepath.Join(l.Dir, sileroFile)
	if b, err := os.ReadFile(p); err == nil && bytes.Equal(b, sileroModel) {
		return p
	}
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return ""
	}
	tmp := p + ".part"
	if err := os.WriteFile(tmp, sileroModel, 0o644); err != nil {
		return ""
	}
	if err := os.Rename(tmp, p); err != nil {
		return ""
	}
	return p
}
