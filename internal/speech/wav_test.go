package speech

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWAVDurationMS(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		size int
		want int64
	}{{44 + 32000*3/2, 1500}, {44, 0}, {10, 0}} {
		p := filepath.Join(dir, "a.wav")
		if err := os.WriteFile(p, make([]byte, c.size), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := WAVDurationMS(p); got != c.want {
			t.Errorf("%d bytes: %d ms, want %d", c.size, got, c.want)
		}
	}
	if got := WAVDurationMS(filepath.Join(dir, "missing.wav")); got != 0 {
		t.Errorf("missing: %d", got)
	}
}
