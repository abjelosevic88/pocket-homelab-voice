package speech

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
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
