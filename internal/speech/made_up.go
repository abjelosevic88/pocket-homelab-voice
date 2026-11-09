package speech

import (
	"bufio"
	"io"
	"os"
	"regexp"
	"strings"
)

// ---- words a speech model made up ----
//
// Speech models write words over silence and, once stuck, repeat one line for
// minutes. A channel that SplitStereo silenced is known to hold nothing there,
// so what was written out over it was not said; a line said again and again
// in a row is the model looping.

// Sounding marks each 100 ms of a mono 16-bit WAV that holds any sound.
func Sounding(path string) ([]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, _ := f.Stat()
	hr := &countReader{r: f}
	rate, channels, _, size, err := wavHeader(hr, fi.Size())
	if err != nil {
		return nil, err
	}
	r := bufio.NewReader(f)
	win := rate / 10 * channels
	buf := make([]byte, 2*win)
	var out []bool
	for done := int64(0); done < size; done += int64(len(buf)) {
		n, rerr := io.ReadFull(r, buf)
		if n < 2 {
			break
		}
		on := false
		for i := 0; i+1 < n; i += 2 {
			if buf[i] != 0 || buf[i+1] != 0 {
				on = true
				break
			}
		}
		out = append(out, on)
		if rerr != nil {
			break
		}
	}
	return out, nil
}

// silentPadMS is how far around a segment its sound may be: the timings drift.
const silentPadMS = 1000

// DropSilent leaves out the segments over stretches with no sound at all
// (sound from Sounding); without a sound map nothing is dropped.
func DropSilent(segs []Segment, sound []bool) []Segment {
	if len(sound) == 0 {
		return segs
	}
	out := segs[:0:0]
	for _, sg := range segs {
		from := max(0, int((sg.Start-silentPadMS)/100))
		to := min(len(sound)-1, int((sg.End+silentPadMS)/100))
		heard := from > len(sound)-1 // past the end: the timings, not silence
		for i := from; i <= to && !heard; i++ {
			heard = sound[i]
		}
		if heard {
			out = append(out, sg)
		}
	}
	return out
}

var loopRe = regexp.MustCompile(`[^\p{L}\p{N}]+`)

func loopKey(t string) string {
	return strings.TrimSpace(loopRe.ReplaceAllString(strings.ToLower(t), " "))
}

// loopRun is how many times in a row one line may be said before it is the
// model looping ("Hello? Hello?" is said; the same line twenty times is not).
const loopRun = 3

// DropLoops keeps the first of a line said loopRun or more times in a row
// and leaves out the rest.
func DropLoops(segs []Segment) []Segment {
	out := segs[:0:0]
	for i := 0; i < len(segs); {
		k := loopKey(segs[i].Text)
		j := i + 1
		for k != "" && j < len(segs) && loopKey(segs[j].Text) == k {
			j++
		}
		if j-i >= loopRun {
			out = append(out, segs[i])
		} else {
			out = append(out, segs[i:j]...)
		}
		i = j
	}
	return out
}
