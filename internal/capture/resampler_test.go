package capture

import (
	"encoding/binary"
	"testing"
)

// makeStereoPCM creates interleaved stereo PCM bytes from left and right int16 slices.
func makeStereoPCM(left, right []int16) []byte {
	if len(left) != len(right) {
		panic("left and right must have same length")
	}
	buf := make([]byte, len(left)*4)
	for i := range left {
		binary.LittleEndian.PutUint16(buf[i*4:], uint16(left[i]))
		binary.LittleEndian.PutUint16(buf[i*4+2:], uint16(right[i]))
	}
	return buf
}

// toInt16 converts PCM bytes back to int16 slice.
func toInt16(b []byte) []int16 {
	samples := make([]int16, len(b)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(b[i*2:]))
	}
	return samples
}

func TestResampleStereoToMono_EmptyInput(t *testing.T) {
	out, err := ResampleStereoToMono([]byte{}, 48000, 16000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty output, got %d bytes", len(out))
	}
}

func TestResampleStereoToMono_ZeroSampleRate(t *testing.T) {
	input := makeStereoPCM([]int16{100}, []int16{200})
	_, err := ResampleStereoToMono(input, 0, 16000)
	if err == nil {
		t.Fatal("expected error for zero input sample rate")
	}

	_, err = ResampleStereoToMono(input, 48000, 0)
	if err == nil {
		t.Fatal("expected error for zero output sample rate")
	}

	_, err = ResampleStereoToMono(input, -1, 16000)
	if err == nil {
		t.Fatal("expected error for negative input sample rate")
	}
}

func TestResampleStereoToMono_OddLength(t *testing.T) {
	// 3 bytes — not a multiple of 4 (one stereo frame).
	input := []byte{1, 2, 3}
	_, err := ResampleStereoToMono(input, 48000, 16000)
	if err == nil {
		t.Fatal("expected error for odd-length input")
	}

	// 6 bytes — not a multiple of 4 (1.5 frames).
	input = []byte{1, 2, 3, 4, 5, 6}
	_, err = ResampleStereoToMono(input, 48000, 16000)
	if err == nil {
		t.Fatal("expected error for input not aligned to stereo frame")
	}
}

func TestResampleStereoToMono_SameRate(t *testing.T) {
	// 48kHz → 48kHz: just stereo to mono conversion, no resampling.
	left := []int16{100, 300, 500}
	right := []int16{200, 400, 600}
	input := makeStereoPCM(left, right)

	out, err := ResampleStereoToMono(input, 48000, 48000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := toInt16(out)
	expected := []int16{
		int16((100 + 200) / 2), // 150
		int16((300 + 400) / 2), // 350
		int16((500 + 600) / 2), // 550
	}

	if len(result) != len(expected) {
		t.Fatalf("expected %d samples, got %d", len(expected), len(result))
	}
	for i := range expected {
		if result[i] != expected[i] {
			t.Errorf("sample[%d]: expected %d, got %d", i, expected[i], result[i])
		}
	}
}

func TestResampleStereoToMono_SingleFrame(t *testing.T) {
	// Single stereo frame at 48kHz → 1 output sample at 16kHz.
	input := makeStereoPCM([]int16{100}, []int16{200})

	out, err := ResampleStereoToMono(input, 48000, 16000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := toInt16(out)
	if len(result) != 1 {
		t.Fatalf("expected 1 output sample, got %d", len(result))
	}
	expected := int16((100 + 200) / 2) // 150
	if result[0] != expected {
		t.Errorf("expected %d, got %d", expected, result[0])
	}
}

func TestResampleStereoToMono_48kHzTo16kHz(t *testing.T) {
	// 6 stereo frames at 48kHz → 2 output samples at 16kHz (ratio 3:1).
	left := []int16{100, 300, 500, 700, 900, 1100}
	right := []int16{200, 400, 600, 800, 1000, 1200}
	input := makeStereoPCM(left, right)

	out, err := ResampleStereoToMono(input, 48000, 16000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := toInt16(out)
	// 6 frames * (16000/48000) = 2 samples exactly.
	if len(result) != 2 {
		t.Fatalf("expected 2 output samples, got %d", len(result))
	}

	// mono = [(100+200)/2, (300+400)/2, (500+600)/2, (700+800)/2, (900+1000)/2, (1100+1200)/2]
	//       = [150, 350, 550, 750, 950, 1150]
	// Output samples at indices 0 and 3 (since 48k/16k = 3):
	expected := []int16{150, 750}
	for i := range expected {
		if result[i] != expected[i] {
			t.Errorf("sample[%d]: expected %d, got %d", i, expected[i], result[i])
		}
	}
}

func TestResampleStereoToMono_WithInterpolation(t *testing.T) {
	// 44.1kHz → 16kHz forces linear interpolation (non-integer ratio).
	left := []int16{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	right := []int16{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	input := makeStereoPCM(left, right)

	out, err := ResampleStereoToMono(input, 44100, 16000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := toInt16(out)
	// 10 frames * 16000/44100 = 3.628... → 4 output samples.
	if len(result) != 4 {
		t.Fatalf("expected 4 output samples, got %d", len(result))
	}

	// mono = [10, 20, 30, 40, 50, 60, 70, 80, 90, 100]
	// Expected via linear interpolation:
	// i=0: pos=0.0   → 10
	// i=1: pos=2.75625 → idx=2, frac=0.75625
	//      val = 30*(1-0.75625) + 40*0.75625 = 7.3125 + 30.25 = 37.5625 → 37
	// i=2: pos=5.5125 → idx=5, frac=0.5125
	//      val = 60*0.4875 + 70*0.5125 = 29.25 + 35.875 = 65.125 → 65
	// i=3: pos=8.26875 → idx=8, frac=0.26875
	//      val = 90*0.73125 + 100*0.26875 = 65.8125 + 26.875 = 92.6875 → 92
	expected := []int16{10, 37, 65, 92}
	for i := range expected {
		if result[i] != expected[i] {
			t.Errorf("sample[%d]: expected %d, got %d", i, expected[i], result[i])
		}
	}
}

func TestResampleStereoToMono_16kHzTo8kHz(t *testing.T) {
	// Downsampling test: 16kHz → 8kHz with 4 frames = 2 output samples.
	left := []int16{100, 200, 300, 400}
	right := []int16{150, 250, 350, 450}
	input := makeStereoPCM(left, right)

	out, err := ResampleStereoToMono(input, 16000, 8000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := toInt16(out)
	if len(result) != 2 {
		t.Fatalf("expected 2 output samples, got %d", len(result))
	}

	// mono = [125, 225, 325, 425]
	// output samples at indices 0 and 2:
	expected := []int16{125, 325}
	for i := range expected {
		if result[i] != expected[i] {
			t.Errorf("sample[%d]: expected %d, got %d", i, expected[i], result[i])
		}
	}
}

func TestResampleStereoToMono_Upsampling(t *testing.T) {
	// Upsampling test: 8kHz → 16kHz with 2 frames.
	left := []int16{100, 300}
	right := []int16{200, 400}
	input := makeStereoPCM(left, right)

	out, err := ResampleStereoToMono(input, 8000, 16000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result := toInt16(out)
	// 2 frames * 16000/8000 = 4 samples.
	if len(result) != 4 {
		t.Fatalf("expected 4 output samples, got %d", len(result))
	}

	// mono = [150, 350]
	// i=0: pos=0.0 → mono[0]=150
	// i=1: pos=0.5, idx=0, frac=0.5 → 150*0.5+350*0.5=250
	// i=2: pos=1.0, idx=1, frac=0.0 → mono[1]=350
	// i=3: pos=1.5, idx=1, frac=0.5 → 350*0.5+350*0.5=350 (clamped to last)
	expected := []int16{150, 250, 350, 350}
	for i := range expected {
		if result[i] != expected[i] {
			t.Errorf("sample[%d]: expected %d, got %d", i, expected[i], result[i])
		}
	}
}
