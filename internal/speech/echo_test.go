package speech

import (
	"encoding/binary"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// A meeting on the laptop's speakers: the microphone hears the call 30 ms
// late and 16 dB quieter. Where only the call speaks, the person's side must
// come out silent; where the person speaks (alone or over the call), it stays.
func TestSplitStereoRemovesEcho(t *testing.T) {
	const rate = 16000
	sec := func(s float64) int { return int(s * rate) }
	n := sec(60)
	rnd := rand.New(rand.NewSource(1))
	speech := func(amp float64) func(int) float64 { // noise with a syllable-like wobble
		return func(i int) float64 {
			return amp * (rnd.Float64()*2 - 1) * (0.6 + 0.4*math.Sin(float64(i)/400))
		}
	}
	call := make([]float64, n)
	for i := range call {
		if i < sec(40) || i >= sec(50) { // the others talk, except 40–50 s
			call[i] = speech(0.25)(i)
		}
	}
	mic := make([]float64, n)
	for i := range mic {
		if i >= 480 {
			mic[i] = 0.16 * call[i-480] // the echo
		}
		if (i >= sec(42) && i < sec(48)) || (i >= sec(52) && i < sec(56)) { // you: alone, then over the others
			mic[i] += speech(0.3)(i)
		}
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "in.wav")
	writeStereo(t, in, rate, mic, call)
	left, right := filepath.Join(dir, "l.wav"), filepath.Join(dir, "r.wav")
	channels, voiced, echo, err := SplitStereo(in, left, right)
	if err != nil || channels != 2 {
		t.Fatalf("split: %d channels, %v", channels, err)
	}
	if echo < 30_000 {
		t.Errorf("echo removed %d ms, want most of the 50 s the call talks alone", echo)
	}
	out := readMono(t, left)
	loud := func(from, to float64) float64 {
		var s float64
		for _, v := range out[sec(from):sec(to)] {
			s += v * v
		}
		return math.Sqrt(s / float64(sec(to)-sec(from)))
	}
	if l := loud(5, 38); l > 0.001 {
		t.Errorf("only the call speaks (5–38 s): your side has %.4f RMS, want silence", l)
	}
	if l := loud(42.5, 47.5); l < 0.05 {
		t.Errorf("you alone (42–48 s): %.4f RMS, want your voice kept", l)
	}
	if l := loud(52.5, 55.5); l < 0.05 {
		t.Errorf("you over the others (52–56 s): %.4f RMS, want your voice kept", l)
	}
	if voiced[1] < 45_000 {
		t.Errorf("the call's side: %d ms voiced, want it untouched", voiced[1])
	}
}

// With headphones the microphone doesn't hear the call: nothing is taken off.
func TestEchoOnlyLeavesHeadphoneCallsAlone(t *testing.T) {
	mic := make([]float64, 600)
	call := make([]float64, 600)
	for i := range call {
		call[i] = 0.2
		mic[i] = 0.0005 // the room's hiss
	}
	if e := echoOnly(mic, call); e != nil {
		t.Errorf("headphones: %d windows marked as echo, want none", len(e))
	}
}

func writeStereo(t *testing.T, path string, rate int, l, r []float64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data := len(l) * 4
	h := make([]byte, 44)
	copy(h, "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+data))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 2)
	binary.LittleEndian.PutUint32(h[24:], uint32(rate))
	binary.LittleEndian.PutUint32(h[28:], uint32(rate*4))
	binary.LittleEndian.PutUint16(h[32:], 4)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(data))
	b := make([]byte, data)
	clip := func(v float64) uint16 { return uint16(int16(math.Max(-1, math.Min(1, v)) * 32767)) }
	for i := range l {
		binary.LittleEndian.PutUint16(b[4*i:], clip(l[i]))
		binary.LittleEndian.PutUint16(b[4*i+2:], clip(r[i]))
	}
	if _, err := f.Write(append(h, b...)); err != nil {
		t.Fatal(err)
	}
}

func readMono(t *testing.T, path string) []float64 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b = b[44:]
	out := make([]float64, len(b)/2)
	for i := range out {
		out[i] = float64(int16(binary.LittleEndian.Uint16(b[2*i:]))) / 32768
	}
	return out
}
