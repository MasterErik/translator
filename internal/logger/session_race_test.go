package logger_test

import (
	"sync"
	"testing"

	"github.com/mastererik/translator/internal/common"
)

// TestConcurrentLogTextAndSaveAudioChunk verifies that concurrent calls to
// LogText and SaveAudioChunk do not cause data races.
// Run with: go test -race ./internal/logger/...
func TestConcurrentLogTextAndSaveAudioChunk(t *testing.T) {
	l, _ := newTempLogger(t)

	const (
		numWriters   = 8
		callsPerWriter = 100
	)

	var wg sync.WaitGroup
	wg.Add(numWriters)

	for i := 0; i < numWriters; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < callsPerWriter; j++ {
				// Interleave text and audio writes.
				if j%2 == 0 {
					_ = l.LogText(common.STTEvent{
						Text:      "concurrent",
						IsFinal:   true,
						ChannelID: "speaker",
					})
				} else {
					_ = l.SaveAudioChunk("speaker", []byte{byte(id), byte(j)})
				}
			}
		}(i)
	}

	wg.Wait()

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestConcurrentMixedChannels verifies that concurrent writes to different
// audio channels do not race.
func TestConcurrentMixedChannels(t *testing.T) {
	l, _ := newTempLogger(t)

	const numWriters = 6

	var wg sync.WaitGroup
	// audio writers: 2 channels × numWriters  +  text writers: numWriters
	wg.Add(numWriters*2 + numWriters)

	channels := []string{"speaker", "mic"}

	for _, ch := range channels {
		for i := 0; i < numWriters; i++ {
			go func(channel string, id int) {
				defer wg.Done()
				for j := 0; j < 50; j++ {
					_ = l.SaveAudioChunk(channel, []byte{byte(id), byte(j)})
				}
			}(ch, i)
		}
	}

	for i := 0; i < numWriters; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = l.LogText(common.STTEvent{
					Text:      "mixed channels",
					ChannelID: channels[id%2],
					IsFinal:   j%10 == 0,
				})
			}
		}(i)
	}

	wg.Wait()

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestConcurrentCloseAndWrite verifies that calling Close concurrently with
// LogText / SaveAudioChunk does not panic or race.
func TestConcurrentCloseAndWrite(t *testing.T) {
	l, _ := newTempLogger(t)

	var wg sync.WaitGroup
	wg.Add(3)

	// Writer goroutines.
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = l.LogText(common.STTEvent{Text: "pre-close"})
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = l.SaveAudioChunk("speaker", []byte{byte(i)})
		}
	}()

	// Close goroutine — fires after a short delay to let writes queue up.
	go func() {
		defer wg.Done()
		_ = l.Close()
	}()

	wg.Wait()
	// If we reach here without a panic, the test passes.
}
