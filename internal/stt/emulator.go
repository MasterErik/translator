package stt

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/mastererik/translator/internal/common"
)

// Default delays for the Gladia emulator (realistic per docs/TESTING.md SLA).
const (
	defaultConnectDelay      = 200 * time.Millisecond // POST + WS dial ≤ 2s SLA
	defaultInterimDelay      = 80 * time.Millisecond  // первый interim — быстро
	defaultFinalDelay        = 400 * time.Millisecond // EndOfTurn ≤ 1s SLA
	defaultTranslationDelay  = 15 * time.Millisecond  // почти мгновенно (10-50ms)
	defaultJitter            = 50 * time.Millisecond  // общий jitter для STT-этапов
	defaultTranslationJitter = 5 * time.Millisecond   // jitter для перевода (узкий)
)

// GladiaEmulator реализует stt.STTProvider, эмулируя поведение Gladia API
// с заранее известным текстом и переводом. Позволяет тестировать
// распределение задержек без реального API.
type GladiaEmulator struct {
	text        string // известный текст (английский)
	translation string // известный перевод
	targetLang  string

	audioCh   chan []byte
	textCh    chan common.STTEvent
	audioDone chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex

	// Настраиваемые задержки (значения по умолчанию — см. константы).
	ConnectDelay      time.Duration
	InterimDelay      time.Duration
	FinalDelay        time.Duration
	TranslationDelay  time.Duration
	Jitter            time.Duration // ± jitter для STT-этапов
	TranslationJitter time.Duration // ± jitter для перевода (отдельно, уже)
}

// NewGladiaEmulator создаёт новый эмулятор Gladia.
// Параметры:
//   - text: известный текст на английском
//   - translation: известный перевод (на русский)
//   - targetLang: целевой язык (опционально, по умолчанию "ru")
func NewGladiaEmulator(text, translation, targetLang string) *GladiaEmulator {
	if targetLang == "" {
		targetLang = "ru"
	}
	return &GladiaEmulator{
		text:              text,
		translation:       translation,
		targetLang:        targetLang,
		audioCh:           make(chan []byte, 64),
		textCh:            make(chan common.STTEvent, 32),
		audioDone:         make(chan struct{}),
		ConnectDelay:      defaultConnectDelay,
		InterimDelay:      defaultInterimDelay,
		FinalDelay:        defaultFinalDelay,
		TranslationDelay:  defaultTranslationDelay,
		Jitter:            defaultJitter,
		TranslationJitter: defaultTranslationJitter,
	}
}

// Start инициализирует эмулятор: симулирует задержку коннекта,
// затем запускает горутину обработки аудио и генерации событий.
func (g *GladiaEmulator) Start(ctx context.Context) error {
	g.mu.Lock()
	if g.cancel != nil {
		g.mu.Unlock()
		return fmt.Errorf("emulator: already started")
	}
	g.ctx, g.cancel = context.WithCancel(ctx)
	g.mu.Unlock()

	// Эмуляция задержки коннекта (симуляция POST + WS dial).
	connectDelay := jitter(g.ConnectDelay, g.Jitter)
	select {
	case <-time.After(connectDelay):
	case <-g.ctx.Done():
		return fmt.Errorf("emulator: connect cancelled: %w", g.ctx.Err())
	}

	go g.run()
	return nil
}

// Stop останавливает эмулятор: отменяет контекст и закрывает textCh.
// Идемпотентен — повторные вызовы безопасны.
func (g *GladiaEmulator) Stop() error {
	g.mu.Lock()
	if g.cancel == nil {
		g.mu.Unlock()
		return nil
	}
	g.cancel()
	g.mu.Unlock()
	return nil
}

// AudioStream возвращает канал для отправки PCM-аудио.
func (g *GladiaEmulator) AudioStream() chan<- []byte {
	return g.audioCh
}

// TextStream возвращает канал для чтения событий STTEvent.
func (g *GladiaEmulator) TextStream() <-chan common.STTEvent {
	return g.textCh
}

// run — основная горутина эмулятора.
// Фаза 1: накопление аудио-байт (~1 секунда аудио).
// Фаза 2: запуск sendEvents и параллельное дренирование audioCh.
// Закрывает textCh только после завершения sendEvents.
func (g *GladiaEmulator) run() {
	defer close(g.audioCh)

	// Фаза 1: накопление аудио. Минимум 1 секунда @ 16kHz mono 16-bit = 32000 байт.
	const minAudioBytes = 16000 * 2 * 1 // 1 second
	totalBytes := 0
	for totalBytes < minAudioBytes {
		select {
		case <-g.ctx.Done():
			close(g.textCh)
			return
		case chunk, ok := <-g.audioCh:
			if !ok {
				close(g.textCh)
				return
			}
			totalBytes += len(chunk)
		}
	}

	// Фаза 2: запускаем sendEvents и параллельно дренируем audioCh.
	eventsDone := make(chan struct{})
	go func() {
		defer close(eventsDone)
		g.sendEvents()
	}()

	// Дренируем оставшееся аудио, чтобы воркер не блокировался.
	g.drainAudio(eventsDone)

	// Ждём завершения sendEvents перед закрытием textCh.
	<-eventsDone
	close(g.textCh)
}

// drainAudio читает audioCh пока не закроется eventsDone или не отменится контекст.
func (g *GladiaEmulator) drainAudio(eventsDone <-chan struct{}) {
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-eventsDone:
			// События отправлены, но продолжаем дренировать остатки аудио кратко.
			g.drainRemaining()
			return
		case _, ok := <-g.audioCh:
			if !ok {
				return
			}
			// Потребляем, но не считаем.
		case <-time.After(200 * time.Millisecond):
			// Нет аудио; на следующей итерации перепроверим eventsDone.
		}
	}
}

// drainRemaining вычитывает оставшиеся чанки из audioCh без блокировки надолго.
func (g *GladiaEmulator) drainRemaining() {
	for {
		select {
		case <-g.ctx.Done():
			return
		case _, ok := <-g.audioCh:
			if !ok {
				return
			}
		default:
			return
		}
	}
}

// sendEvents отправляет 2-3 промежуточных (interim) события,
// затем финальное (EndOfTurn) и перевод (translation).
// Первое interim отправляется после задержки InterimDelay (± jitter).
func (g *GladiaEmulator) sendEvents() {
	words := strings.Fields(g.text)
	if len(words) == 0 {
		return
	}

	// Задержка перед первым interim.
	delay := jitter(g.InterimDelay, g.Jitter)
	if !sleepCtx(g.ctx, delay) {
		return
	}

	// 2 промежуточных события с нарастающим текстом.
	numInterims := 2
	for i := 1; i <= numInterims; i++ {
		partialLen := len(words) * i / (numInterims + 1)
		if partialLen < 1 {
			partialLen = 1
		}
		partial := strings.Join(words[:partialLen], " ")

		g.emit(common.STTEvent{
			Text:      partial,
			Event:     common.EventUpdate,
			ChannelID: "speaker",
			Timestamp: time.Now(),
		})

		// Задержка перед следующим interim.
		if i < numInterims {
			delay = jitter(g.InterimDelay, g.Jitter)
			if !sleepCtx(g.ctx, delay) {
				return
			}
		}
	}

	// Финальное событие (EndOfTurn).
	delay = jitter(g.FinalDelay, g.Jitter)
	if !sleepCtx(g.ctx, delay) {
		return
	}
	g.emit(common.STTEvent{
		Text:      g.text,
		Event:     common.EventEndOfTurn,
		ChannelID: "speaker",
		Timestamp: time.Now(),
	})

	// Перевод (translation) — почти мгновенно, отдельный узкий jitter.
	delay = jitter(g.TranslationDelay, g.TranslationJitter)
	if !sleepCtx(g.ctx, delay) {
		return
	}
	g.emit(common.STTEvent{
		Text:      g.translation,
		Event:     common.EventEndOfTurn,
		ChannelID: "translation",
		Timestamp: time.Now(),
	})
}

// emit отправляет событие в textCh. Если канал заполнен или контекст отменён,
// событие отбрасывается.
func (g *GladiaEmulator) emit(evt common.STTEvent) {
	select {
	case g.textCh <- evt:
	case <-g.ctx.Done():
	default:
	}
}

// jitter возвращает задержку = base + случайная величина ± jitter,
// с защитой от отрицательных значений (минимум — 1ms).
func jitter(base, jitterRange time.Duration) time.Duration {
	if base <= 0 {
		base = 1 * time.Millisecond
	}
	if jitterRange <= 0 {
		return base
	}
	// Используем нормальное распределение: base + N(0, jitter/2).
	// Clamp к [base - jitter, base + jitter].
	jitterVal := time.Duration(rand.NormFloat64() * float64(jitterRange) / 2.0)
	result := base + jitterVal
	minVal := base - jitterRange
	if minVal < 1*time.Millisecond {
		minVal = 1 * time.Millisecond
	}
	maxVal := base + jitterRange
	if result < minVal {
		result = minVal
	}
	if result > maxVal {
		result = maxVal
	}
	return result
}

// sleepCtx — time.Sleep с поддержкой отмены контекста.
// Возвращает false, если контекст был отменён.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

// Compile-time interface check.
var _ STTProvider = (*GladiaEmulator)(nil)
