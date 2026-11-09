package speech

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestDropLoops(t *testing.T) {
	var segs []Segment
	segs = append(segs, Segment{Start: 0, End: 1000, Text: "Hello?"}, Segment{Start: 1000, End: 2000, Text: "Hello?"})
	for i := 0; i < 20; i++ {
		segs = append(segs, Segment{Start: int64(3000 + i*1000), End: int64(4000 + i*1000), Text: "And they're going to help you."})
	}
	segs = append(segs, Segment{Start: 30000, End: 31000, Text: "Let's start."})
	got := DropLoops(segs)
	if len(got) != 4 {
		t.Fatalf("got %d segments: %+v", len(got), got)
	}
	if got[0].Text != "Hello?" || got[1].Text != "Hello?" || got[2].Text != "And they're going to help you." || got[3].Text != "Let's start." {
		t.Fatalf("wrong lines kept: %+v", got)
	}
}

func TestDropSilent(t *testing.T) {
	// 10 s of which only 2–3 s has sound
	dir := t.TempDir()
	p := filepath.Join(dir, "a.wav")
	rate := 16000
	samples := make([]byte, 2*rate*10)
	for i := 2 * rate; i < 3*rate; i++ {
		binary.LittleEndian.PutUint16(samples[2*i:], uint16(1000))
	}
	f, _ := os.Create(p)
	_ = writeWAVHeader(f, rate, int64(len(samples)))
	_, _ = f.Write(samples)
	f.Close()
	sound, err := Sounding(p)
	if err != nil || len(sound) != 100 {
		t.Fatalf("sounding: %d %v", len(sound), err)
	}
	segs := []Segment{
		{Start: 2100, End: 2900, Text: "said"},
		{Start: 3500, End: 3800, Text: "near enough"}, // within the timing slack
		{Start: 6000, End: 9000, Text: "made up"},
	}
	got := DropSilent(segs, sound)
	if len(got) != 2 || got[1].Text != "near enough" {
		t.Fatalf("got %+v", got)
	}
}
