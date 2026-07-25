package capture

// defaultBufferSizeMs is the default audio buffer period in milliseconds.
// Flux recommends 80ms chunks for optimal turn detection.
const defaultBufferSizeMs = 80

// channelBufferSize — ёмкость выходных аудиоканалов.
// 64 × 80ms = ~5 секунд буфера.
const channelBufferSize = 64

// CaptureConfig holds the configuration for dual-channel audio capture.
type CaptureConfig struct {
	// LoopbackDeviceName is the name of the WASAPI loopback device to use.
	// If empty, the default playback device is used for loopback capture.
	LoopbackDeviceName string

	// MicDeviceName is the name of the microphone device to use.
	// If empty, the default capture device is used.
	MicDeviceName string

	// BufferSizeMs is the audio buffer period in milliseconds.
	// This determines callback frequency. Default is 20ms.
	BufferSizeMs int
}
