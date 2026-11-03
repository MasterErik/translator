package translator

import (
	"strings"
	"sync"
	"time"

	"github.com/mastererik/translator/internal/common"
)

// SpeechRemark — одна финальная реплика интервьюера (EndOfTurn), вопрос или нет.
type SpeechRemark struct {
	// Seq — монотонный номер записи (0 на первой). Присваивается глобально по
	// SpeechLog и НЕ переиспользуется после вытеснения реплики из кольца.
	Seq uint64
	// Text — TrimSpace-нормализованный текст реплики.
	Text string
	// Timestamp — момент завершения реплики (EndOfTurn).
	Timestamp time.Time
}

// speechEntry — внутренняя запись кольца: реплика + флаг потребления.
type speechEntry struct {
	remark   SpeechRemark
	consumed bool
}

// SpeechLog — потоко-безопасный (sync.Mutex) кольцевой буфер финальных реплик
// интервьюера (EndOfTurn), вопрос или нет.
//
// Лог — «полный разговорный контекст» текущего интервью (план fix-plan-2026-11,
// F1, блоки A1/A5): каждая финальная реплика интервьюера попадает сюда, чтобы
// блок «Interviewer remarks» в user-промпте не терял не-вопросы.
//
// Флаг потребления относится ТОЛЬКО к отдельному потоку уточнений
// (Shift+F2 / авто-clarify: TakeLatestUnconsumed / SnapshotAfter+Consume),
// но НЕ к рендеру контекста: потреблённая реплика продолжает попадать
// в RecentContext — как разговорная история она не исчезает.
type SpeechLog struct {
	mu sync.Mutex

	remarks   []speechEntry // кольцо: при переполнении вытесняются самые старые записи
	capacity  int           // максимум реплик в кольце (default 20)
	maxTokens int           // дефолт бюджета RecentContext при maxTokens<=0 (default 1500)
	nextSeq   uint64        // Seq следующей записи (глобально монотонный)
}

const (
	// defaultSpeechCapacity — размер кольца при capacity<=0.
	defaultSpeechCapacity = 20
	// defaultSpeechTokens — бюджет блока реплик при maxTokens<=0.
	defaultSpeechTokens = 1500
)

// NewSpeechLog создаёт SpeechLog с заданными лимитами.
// Нулевые/отрицательные значения заменяются на значения по умолчанию
// (capacity<=0 → 20 записей; maxTokens<=0 → 1500 токенов).
func NewSpeechLog(capacity, maxTokens int) *SpeechLog {
	if capacity <= 0 {
		capacity = defaultSpeechCapacity
	}
	if maxTokens <= 0 {
		maxTokens = defaultSpeechTokens
	}
	return &SpeechLog{
		remarks:   make([]speechEntry, 0, capacity),
		capacity:  capacity,
		maxTokens: maxTokens,
	}
}

// Add записывает финальную реплику интервьюера (EndOfTurn) в кольцо.
//
// Пустая (после TrimSpace) реплика — no-op. Нулевая Timestamp заменяется на
// time.Now() (защита от вызывающих без метки времени: иначе реплика навсегда
// выпадает из всех окон SnapshotAfter, которые смотрят в будущее).
// При превышении capacity самые старые записи вытесняются из кольца;
// Seq присваивается глобально монотонно и не переиспользуется.
func (s *SpeechLog) Add(text string, ts time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	if ts.IsZero() {
		ts = time.Now()
	}

	s.remarks = append(s.remarks, speechEntry{
		remark: SpeechRemark{Seq: s.nextSeq, Text: trimmed, Timestamp: ts},
	})
	s.nextSeq++
	if len(s.remarks) > s.capacity {
		// Вытеснение из кольца: самая старая запись уходит с начала.
		s.remarks = s.remarks[len(s.remarks)-s.capacity:]
	}
}

// RecentContext рендерит блок реплик интервьюера для user-промпта.
//
// Формат:
//
//	Interviewer remarks:
//	- <реплика>
//	- <реплика>
//
// Берутся последние maxRemarks реплик В хронологическом порядке (старые сверху).
// Обрезка по бюджету maxTokens (оценка common.EstimateTokens по строкам блока)
// идёт с конца: самые старые реплики отбрасываются, самая свежая сохраняется
// всегда — семантика ConversationHistory.BuildContext.
// Consumed-реплики попадают в блок наравне с unconsumed (см. SpeechLog).
// Пустая история → "".
//
// maxRemarks<=0 → размер кольца; maxTokens<=0 → дефолт из NewSpeechLog.
func (s *SpeechLog) RecentContext(maxRemarks, maxTokens int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.remarks) == 0 {
		return ""
	}
	if maxRemarks <= 0 {
		maxRemarks = s.capacity
	}
	if maxTokens <= 0 {
		maxTokens = s.maxTokens
	}

	// Отбираем с конца (самые свежие реплики), пока укладываемся в лимиты.
	// Самая свежая реплика сохраняется всегда (первое добавление без допуска).
	selected := make([]speechEntry, 0, len(s.remarks))
	total := 0
	for i := len(s.remarks) - 1; i >= 0 && len(selected) < maxRemarks; i-- {
		line := "- " + s.remarks[i].remark.Text
		tk := common.EstimateTokens(line)
		if len(selected) == 0 || total+tk <= maxTokens {
			selected = append(selected, s.remarks[i])
			total += tk
			continue
		}
		break
	}

	var sb strings.Builder
	sb.WriteString("Interviewer remarks:")
	// selected собраны от свежего к старому — печатаем в хронологическом порядке.
	for i := len(selected) - 1; i >= 0; i-- {
		sb.WriteString("\n- ")
		sb.WriteString(selected[i].remark.Text)
	}
	return sb.String()
}

// TakeLatestUnconsumed возвращает текст ПОСЛЕДНЕЙ unconsumed-реплики и
// помечает её consumed. Нет такой → ("", false).
//
// Замена старых setLastFinal/takeLastFinal (F3-буфер уточнений): потреблённая
// реплика больше не возвращается, но более старые unconsumed-реплики остаются
// в очереди — они забираются следующими вызовами. Когда очередь пуста
// (повторный Shift+F2 без новой фразы) — no-op.
func (s *SpeechLog) TakeLatestUnconsumed() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := len(s.remarks) - 1; i >= 0; i-- {
		if s.remarks[i].consumed {
			continue
		}
		s.remarks[i].consumed = true
		return s.remarks[i].remark.Text, true
	}
	return "", false
}

// Restore возвращает потреблённый текст как НОВУЮ unconsumed-реплику
// (ts = time.Now(), новый Seq) — семантика C2: уточнение не должно
// теряться безвозвратно. Порядок истории не переписывается задним числом:
// восстановленная реплика встаёт в хвост кольца. Пустой текст — no-op (как в Add).
func (s *SpeechLog) Restore(text string) {
	s.Add(text, time.Now())
}

// SnapshotAfter возвращает КОПИИ всех unconsumed-реплик с Timestamp >= ts
// (старые первыми). Consumed-реплики в срез не попадают: поток уточнений
// берёт только ещё не обработанные фрагменты речи.
func (s *SpeechLog) SnapshotAfter(ts time.Time) []SpeechRemark {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []SpeechRemark
	for i := range s.remarks {
		e := s.remarks[i]
		if e.consumed || e.remark.Timestamp.Before(ts) {
			continue
		}
		out = append(out, e.remark)
	}
	return out
}

// Consume помечает реплику consumed по Seq. false — нет записи с таким Seq
// (в т.ч. вытесненной из кольца) или она уже consumed.
func (s *SpeechLog) Consume(seq uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.remarks {
		if s.remarks[i].remark.Seq != seq {
			continue
		}
		if s.remarks[i].consumed {
			return false
		}
		s.remarks[i].consumed = true
		return true
	}
	return false
}
