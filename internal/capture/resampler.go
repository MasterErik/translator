// Package capture provides dual-channel audio capture via malgo WASAPI
// loopback and microphone devices, with resampling from 48kHz Stereo to
// 16kHz Mono.
package capture

import (
	"encoding/binary"
	"fmt"
)

// defaultInputRate is the sample rate expected from malgo capture devices
// configured for 48kHz stereo.
const defaultInputRate = 48000

// defaultOutputRate is the target sample rate for downstream STT processing.
const defaultOutputRate = 16000

// bytesPerSample is the size of one int16 sample in bytes.
const bytesPerSample = 2

// ResampleStereoToMono converts interleaved stereo PCM (int16 L,R pairs)
// at inputSampleRate to mono PCM (int16) at outputSampleRate using linear
// interpolation. Each input frame is a pair of int16 values — left and right
// channels. The output is a single mono channel created by averaging the
// stereo channels at each resampled point.
//
// Edge cases:
//   - Empty input returns an empty slice.
//   - Odd-length input returns an error (incomplete stereo frame).
//   - Zero inputSampleRate or outputSampleRate returns an error.
//   - When inputSampleRate equals outputSampleRate, only stereo-to-mono
//     conversion is performed (no interpolation).
func ResampleStereoToMono(input []byte, inputSampleRate int, outputSampleRate int) ([]byte, error) {
	if len(input) == 0 {
		return []byte{}, nil
	}

	if inputSampleRate <= 0 {
		return nil, fmt.Errorf("resample: input sample rate must be positive, got %d", inputSampleRate)
	}
	if outputSampleRate <= 0 {
		return nil, fmt.Errorf("resample: output sample rate must be positive, got %d", outputSampleRate)
	}

	// Each stereo frame is 2 int16 samples = 4 bytes.
	if len(input)%(2*bytesPerSample) != 0 {
		return nil, fmt.Errorf("resample: input length %d is not a multiple of %d bytes (incomplete stereo frame)",
			len(input), 2*bytesPerSample)
	}

	// Convert bytes to int16 slice.
	sampleCount := len(input) / bytesPerSample
	samples := make([]int16, sampleCount)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(input[i*bytesPerSample:]))
	}

	// Number of stereo frames.
	frameCount := sampleCount / 2

	// Pre-compute mono values for each frame (average of L and R).
	mono := make([]int16, frameCount)
	for i := 0; i < frameCount; i++ {
		// Average L and R, rounding towards zero (truncation is fine for audio).
		l := int32(samples[i*2])
		r := int32(samples[i*2+1])
		mono[i] = int16((l + r) / 2)
	}

	// Calculate number of output samples.
	// Duration in seconds = frameCount / inputSampleRate.
	// Output samples = duration * outputSampleRate.
	outputSamples := int(int64(frameCount) * int64(outputSampleRate) / int64(inputSampleRate))

	// Handle the case where the last fractional sample rounds up.
	remainder := int(int64(frameCount)*int64(outputSampleRate)) % int(inputSampleRate)
	if remainder > 0 {
		outputSamples++
	}

	out := make([]int16, outputSamples)
	for i := 0; i < outputSamples; i++ {
		// Position in the input timeline (as a float).
		pos := float64(i) * float64(inputSampleRate) / float64(outputSampleRate)
		idx := int(pos)
		frac := pos - float64(idx)

		if idx >= frameCount-1 {
			// At or past the last frame, use the last frame.
			out[i] = mono[frameCount-1]
		} else {
			// Linear interpolation between mono[idx] and mono[idx+1].
			val := float64(mono[idx])*(1.0-frac) + float64(mono[idx+1])*frac
			out[i] = int16(val)
		}
	}

	// Convert back to bytes.
	output := make([]byte, len(out)*bytesPerSample)
	for i, s := range out {
		binary.LittleEndian.PutUint16(output[i*bytesPerSample:], uint16(s))
	}

	return output, nil
}
