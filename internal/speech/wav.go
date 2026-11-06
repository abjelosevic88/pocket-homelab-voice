package speech

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"sort"
)

// WAVInfo reads a 16-bit PCM WAV: how long it is and the loudness of each
// of n equal slices (0–100) for the waveform.
func WAVInfo(path string, n int) (durationMS int64, peaks []int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var hdr [12]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	if string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" {
		return 0, nil, errors.New("not a WAV file")
	}
	var rate, channels, bits int
	for {
		var ch [8]byte
		if _, err := io.ReadFull(r, ch[:]); err != nil {
			return 0, nil, errors.New("WAV without data")
		}
		size := int64(binary.LittleEndian.Uint32(ch[4:8]))
		switch string(ch[0:4]) {
		case "fmt ":
			b := make([]byte, size)
			if _, err := io.ReadFull(r, b); err != nil || len(b) < 16 {
				return 0, nil, errors.New("bad WAV format chunk")
			}
			channels = int(binary.LittleEndian.Uint16(b[2:4]))
			rate = int(binary.LittleEndian.Uint32(b[4:8]))
			bits = int(binary.LittleEndian.Uint16(b[14:16]))
		case "data":
			if bits != 16 || rate <= 0 || channels <= 0 {
				return 0, nil, errors.New("only 16-bit PCM WAV is read")
			}
			if size == 0 || size == 0xFFFFFFFF {
				fi, _ := f.Stat()
				size = fi.Size() - 44
			}
			frames := size / int64(2*channels)
			durationMS = frames * 1000 / int64(rate)
			peaks = readPeaks(r, frames, channels, n)
			return durationMS, peaks, nil
		default:
			if _, err := io.CopyN(io.Discard, r, size+size%2); err != nil {
				return 0, nil, err
			}
		}
	}
}

func readPeaks(r io.Reader, frames int64, channels, n int) []int {
	if n <= 0 || frames <= 0 {
		return []int{}
	}
	per := frames / int64(n)
	if per < 1 {
		per = 1
	}
	rms := make([]float64, 0, n)
	var sum float64
	var count int64
	buf := make([]byte, 2*channels)
	for i := int64(0); i < frames; i++ {
		if _, err := io.ReadFull(r, buf); err != nil {
			break
		}
		v := float64(int16(binary.LittleEndian.Uint16(buf[0:2]))) / 32768
		sum += v * v
		count++
		if count == per && len(rms) < n {
			rms = append(rms, math.Sqrt(sum/float64(count)))
			sum, count = 0, 0
		}
	}
	if count > 0 && len(rms) < n {
		rms = append(rms, math.Sqrt(sum/float64(count)))
	}
	return ScalePeaks(rms)
}

// ScalePeaks turns loudness values into 0–100 bars, the loudest at 100 and
// quiet speech still visible.
func ScalePeaks(vals []float64) []int {
	top := 0.0
	for _, v := range vals {
		top = math.Max(top, v)
	}
	out := make([]int, len(vals))
	if top <= 0 {
		return out
	}
	for i, v := range vals {
		p := int(math.Round(math.Sqrt(v/top) * 100))
		if p < 2 {
			p = 2
		}
		out[i] = p
	}
	return out
}

// wavHeader reads a WAV header up to the start of the data chunk.
func wavHeader(r io.Reader, fileSize int64) (rate, channels, bits int, dataSize int64, err error) {
	var hdr [12]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return
	}
	if string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" {
		err = errors.New("not a WAV file")
		return
	}
	for {
		var ch [8]byte
		if _, err = io.ReadFull(r, ch[:]); err != nil {
			err = errors.New("WAV without data")
			return
		}
		size := int64(binary.LittleEndian.Uint32(ch[4:8]))
		switch string(ch[0:4]) {
		case "fmt ":
			b := make([]byte, size)
			if _, err = io.ReadFull(r, b); err != nil || len(b) < 16 {
				err = errors.New("bad WAV format chunk")
				return
			}
			channels = int(binary.LittleEndian.Uint16(b[2:4]))
			rate = int(binary.LittleEndian.Uint32(b[4:8]))
			bits = int(binary.LittleEndian.Uint16(b[14:16]))
		case "data":
			if bits != 16 || rate <= 0 || channels <= 0 {
				err = errors.New("only 16-bit PCM WAV is read")
				return
			}
			if size == 0 || size == 0xFFFFFFFF {
				size = fileSize - 44
			}
			dataSize = size
			return
		default:
			if _, err = io.CopyN(io.Discard, r, size+size%2); err != nil {
				return
			}
		}
	}
}

// writeWAVHeader writes a 16-bit mono PCM header for n bytes of samples.
func writeWAVHeader(w io.Writer, rate int, n int64) error {
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+n))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 1)
	binary.LittleEndian.PutUint32(h[24:], uint32(rate))
	binary.LittleEndian.PutUint32(h[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(n))
	_, err := w.Write(h)
	return err
}

// SilenceLevel is the loudness (RMS, 0–1) below which a stretch of a channel
// counts as silence: about −42 dBFS.
const SilenceLevel = 0.008

// SplitStereo writes the two channels of a 16-bit stereo WAV as mono WAVs,
// with near-silent stretches (100 ms windows, kept 200 ms around speech)
// made fully silent so the speech model does not invent words in them.
// On the left (a meeting's microphone) the stretches that only hold the call
// coming back through the speakers are silenced too (see echoOnly); echoMS is
// how much of it that was. voicedMS is how much of each channel has sound. A
// recording that is not stereo answers channels and writes nothing.
func SplitStereo(in, left, right string) (channels int, voicedMS [2]int64, echoMS int64, err error) {
	f, err := os.Open(in)
	if err != nil {
		return 0, voicedMS, 0, err
	}
	defer f.Close()
	fi, _ := f.Stat()
	// the header is read unbuffered, so the file stands where the samples begin
	hr := &countReader{r: f}
	rate, channels, _, size, err := wavHeader(hr, fi.Size())
	if err != nil || channels != 2 {
		return channels, voicedMS, echoMS, err
	}
	dataStart := hr.n
	if size > fi.Size()-dataStart {
		size = fi.Size() - dataStart
	}
	r := bufio.NewReader(f)
	win := rate / 10 // 100 ms
	frames := size / 4
	// pass 1: how loud each window of each channel is
	var loud [2][]bool
	var level [2][]float64
	buf := make([]byte, 4*win)
	for done := int64(0); done < frames; done += int64(win) {
		n, rerr := io.ReadFull(r, buf)
		k := n / 4
		if k == 0 {
			break
		}
		var sum [2]float64
		for i := 0; i < k; i++ {
			for c := 0; c < 2; c++ {
				v := float64(int16(binary.LittleEndian.Uint16(buf[4*i+2*c:]))) / 32768
				sum[c] += v * v
			}
		}
		for c := 0; c < 2; c++ {
			rms := math.Sqrt(sum[c] / float64(k))
			level[c] = append(level[c], rms)
			loud[c] = append(loud[c], rms >= SilenceLevel)
		}
		if rerr != nil {
			break
		}
	}
	for i, echo := range echoOnly(level[0], level[1]) {
		if echo && loud[0][i] {
			loud[0][i] = false
			echoMS += int64(win) * 1000 / int64(rate)
		}
	}
	keep := func(c, i int) bool {
		for j := i - 2; j <= i+2; j++ {
			if j >= 0 && j < len(loud[c]) && loud[c][j] {
				return true
			}
		}
		return false
	}
	// pass 2: write each channel, silence where it is quiet
	if _, err := f.Seek(dataStart, io.SeekStart); err != nil {
		return channels, voicedMS, echoMS, err
	}
	r = bufio.NewReader(f)
	outs := [2]*os.File{}
	for c, p := range []string{left, right} {
		o, err := os.Create(p)
		if err != nil {
			return channels, voicedMS, echoMS, err
		}
		defer o.Close()
		outs[c] = o
	}
	ws := [2]*bufio.Writer{bufio.NewWriter(outs[0]), bufio.NewWriter(outs[1])}
	for c := 0; c < 2; c++ {
		if err := writeWAVHeader(ws[c], rate, frames*2); err != nil {
			return channels, voicedMS, echoMS, err
		}
	}
	mono := make([]byte, 2*win)
	var written int64
	for i := 0; written < frames; i++ {
		n, rerr := io.ReadFull(r, buf)
		k := n / 4
		if k == 0 {
			break
		}
		for c := 0; c < 2; c++ {
			on := keep(c, i)
			if on {
				voicedMS[c] += int64(k) * 1000 / int64(rate)
			}
			for j := 0; j < k; j++ {
				if on {
					mono[2*j], mono[2*j+1] = buf[4*j+2*c], buf[4*j+2*c+1]
				} else {
					mono[2*j], mono[2*j+1] = 0, 0
				}
			}
			if _, err := ws[c].Write(mono[:2*k]); err != nil {
				return channels, voicedMS, echoMS, err
			}
		}
		written += int64(k)
		if rerr != nil {
			break
		}
	}
	for c := 0; c < 2; c++ {
		if err := ws[c].Flush(); err != nil {
			return channels, voicedMS, echoMS, err
		}
		if written != frames { // a short file: fix the sizes
			_, _ = outs[c].Seek(0, io.SeekStart)
			_ = writeWAVHeader(outs[c], rate, written*2)
		}
	}
	return channels, voicedMS, echoMS, nil
}

// echoMargin is how much louder than the call's echo the microphone must be
// for a stretch to count as the person talking (dB).
const echoMargin = 9.0

// echoOnly marks the 100 ms windows of a meeting's microphone (mic, RMS per
// window) that hold nothing but the call (call, the computer's sound) heard
// back through the speakers: no louder than the call around them (±200 ms, as
// the two sides of a recording drift a little apart) times how much of the
// call this microphone picks up, plus echoMargin. That pickup is measured on
// the recording itself, as the usual difference wherever the call is loud.
// Nothing is marked when there is too little call to tell (under 30 s) or the
// microphone hardly hears it (headphones).
func echoOnly(mic, call []float64) []bool {
	n := min(len(mic), len(call))
	db := func(v float64) float64 { return 20 * math.Log10(v+1e-9) }
	near := make([]float64, n)
	for i := 0; i < n; i++ {
		near[i] = -200
		for j := max(0, i-2); j <= min(n-1, i+2); j++ {
			near[i] = max(near[i], db(call[j]))
		}
	}
	var diffs []float64
	for i := 0; i < n; i++ {
		if near[i] > -35 && db(mic[i]) > -70 {
			diffs = append(diffs, db(mic[i])-near[i])
		}
	}
	if len(diffs) < 300 {
		return nil
	}
	sort.Float64s(diffs)
	pickup := diffs[len(diffs)/2]
	if pickup < -30 {
		return nil
	}
	out := make([]bool, n)
	for i := 0; i < n; i++ {
		out[i] = near[i] > -45 && db(mic[i]) <= near[i]+pickup+echoMargin
	}
	return out
}

type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
