//go:build cgo

package capture

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gen2brain/malgo"
)

// Capture manages dual-channel audio capture from a WASAPI loopback device
// and a microphone, with resampling from 48kHz stereo to 16kHz mono.
type Capture struct {
	config CaptureConfig

	mu       sync.Mutex
	ctx      *malgo.AllocatedContext
	loopback *malgo.Device
	mic      *malgo.Device
}

// NewCapture creates a new Capture with the given configuration.
func NewCapture(config CaptureConfig) *Capture {
	if config.BufferSizeMs <= 0 {
		config.BufferSizeMs = defaultBufferSizeMs
	}
	return &Capture{config: config}
}

// Start initializes audio devices and begins dual-channel capture.
// Returns loopbackCh (system audio) and micCh (microphone), both 16kHz mono.
// Capture stops when ctx is cancelled; channels are closed after drain.
func (c *Capture) Start(ctx context.Context) (<-chan []byte, <-chan []byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var err error
	c.ctx, err = malgo.InitContext(nil, malgo.ContextConfig{}, func(message string) {})
	if err != nil {
		return nil, nil, fmt.Errorf("capture: init malgo context: %w", err)
	}

	loopbackCh := make(chan []byte, channelBufferSize)
	micCh := make(chan []byte, channelBufferSize)

	if err := c.startLoopback(ctx, loopbackCh); err != nil {
		c.ctx.Uninit()
		c.ctx.Free()
		return nil, nil, fmt.Errorf("capture: start loopback: %w", err)
	}

	if err := c.startMic(ctx, micCh); err != nil {
		c.loopback.Uninit()
		c.ctx.Uninit()
		c.ctx.Free()
		return nil, nil, fmt.Errorf("capture: start microphone: %w", err)
	}

	return loopbackCh, micCh, nil
}

// startLoopback initializes and starts the WASAPI loopback capture device.
func (c *Capture) startLoopback(ctx context.Context, ch chan []byte) error {
	deviceID, err := c.findDevice(malgo.Loopback, c.config.LoopbackDeviceName)
	if err != nil {
		return fmt.Errorf("find loopback device: %w", err)
	}

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Loopback)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = 2
	deviceConfig.SampleRate = 48000
	deviceConfig.PeriodSizeInMilliseconds = uint32(c.config.BufferSizeMs)

	if deviceID != nil {
		deviceConfig.Capture.DeviceID = deviceID.Pointer()
	}

	callbacks := malgo.DeviceCallbacks{
		Data: c.makeDataCallback(ch, 48000, 16000),
	}

	device, err := malgo.InitDevice(c.ctx.Context, deviceConfig, callbacks)
	if err != nil {
		return fmt.Errorf("init loopback device: %w", err)
	}

	if err := device.Start(); err != nil {
		device.Uninit()
		return fmt.Errorf("start loopback device: %w", err)
	}

	c.loopback = device
	go c.monitorShutdown(ctx, device, ch, "loopback")
	return nil
}

// startMic initializes and starts the microphone capture device.
func (c *Capture) startMic(ctx context.Context, ch chan []byte) error {
	deviceID, err := c.findDevice(malgo.Capture, c.config.MicDeviceName)
	if err != nil {
		return fmt.Errorf("find microphone device: %w", err)
	}

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = 2
	deviceConfig.SampleRate = 48000
	deviceConfig.PeriodSizeInMilliseconds = uint32(c.config.BufferSizeMs)

	if deviceID != nil {
		deviceConfig.Capture.DeviceID = deviceID.Pointer()
	}

	callbacks := malgo.DeviceCallbacks{
		Data: c.makeDataCallback(ch, 48000, 16000),
	}

	device, err := malgo.InitDevice(c.ctx.Context, deviceConfig, callbacks)
	if err != nil {
		return fmt.Errorf("init microphone device: %w", err)
	}

	if err := device.Start(); err != nil {
		device.Uninit()
		return fmt.Errorf("start microphone device: %w", err)
	}

	c.mic = device
	go c.monitorShutdown(ctx, device, ch, "microphone")
	return nil
}

// findDevice looks up a device by name or returns nil (default) if name is empty.
func (c *Capture) findDevice(kind malgo.DeviceType, name string) (*malgo.DeviceID, error) {
	if name == "" {
		return nil, nil
	}

	devices, err := c.ctx.Devices(kind)
	if err != nil {
		return nil, fmt.Errorf("enumerate devices: %w", err)
	}

	for i := range devices {
		if devices[i].Name() == name {
			return &devices[i].ID, nil
		}
	}

	return nil, fmt.Errorf("device %q not found", name)
}

// makeDataCallback returns a malgo DataProc that resamples 48kHz stereo → 16kHz mono
// and sends to ch. Non-blocking — drops frames under backpressure.
func (c *Capture) makeDataCallback(ch chan<- []byte, inputRate, outputRate int) malgo.DataProc {
	return func(output, input []byte, framecount uint32) {
		if len(input) == 0 {
			return
		}

		resampled, err := ResampleStereoToMono(input, inputRate, outputRate)
		if err != nil {
			return
		}

		select {
		case ch <- resampled:
		default:
		}
	}
}

// monitorShutdown waits for ctx cancellation, stops the device, drains and closes the channel.
func (c *Capture) monitorShutdown(ctx context.Context, device *malgo.Device, ch chan []byte, name string) {
	<-ctx.Done()

	_ = device.Stop()
	device.Uninit()

	time.Sleep(time.Duration(c.config.BufferSizeMs) * time.Millisecond)

	for {
		select {
		case <-ch:
		default:
			close(ch)
			return
		}
	}
}
