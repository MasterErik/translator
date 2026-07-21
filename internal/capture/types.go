package capture

// defaultBufferSizeMs is the default audio buffer period in milliseconds.
const defaultBufferSizeMs = 20

// channelBufferSize is the capacity of the output audio channels.
// At 20ms buffers, 100 items = 2 seconds of buffered audio.
const channelBufferSize = 100

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
