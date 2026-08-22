# Архитектура ответов на вопросы (LLM Question Answering)

Как Translator генерирует ответы на вопросы интервью через LLM и как это расширяется до личной базы данных (retrieval / RAG).

## Обзор

Приложение реального времени: распознаёт вопрос собеседника (EN), строит промпт из двух независимых контекстов (факты кандидата + история разговора) и просит LLM сгенерировать ответ от первого лица. Ответы выдаются последовательно через одну горутину-потребитель.

Ключевой принцип: **LLM не хранит память** — контекст собирается на стороне приложения и передаётся в каждом запросе.

## Поток ответа на вопрос

```
EndOfTurn (финальный транскрипт, ChannelID != "translation")
    │
    ▼
translator.IsQuestion(text)?
    ├── нет → ничего
    └── да
        ▼
dispatcher.enqueueQuestion(question)        // неблокирующе
        ▼
answerCh (chan string, buf 16)
        ▼
answerWorker (ОДНА горутина, FIFO, дедупликация подряд)
        │  (обрабатывает и вопросы, и команды F1–F4, и Esc)
        ▼
generateAnswers(question, command)
        │
        ├─ ConversationContext = history.BuildContext()   // история интервью
        │
        ▼
AnswerRequest{Question, CandidateContext, ConversationContext, Command}
        ▼
├─ BuildAnswerPrompt(req)          // user prompt
│     "Recent conversation: …\nThe interviewer asked: <question>\n…[модификатор]"
├─ buildSystemPrompt(candidate)    // system prompt
│     SystemPromptAnswerGen + "\n\nCandidate context:\n" + candidate
        ▼
llm.GenerateAnswers(ctx, req)
        ▼
parseAnswerHints(content) → ["EN: <en> | RU: <ru>", …]
        ▼
overlay.AddMessage(AnswerCandidates)
        ▼
history.RecordAnswer(question, answers[0])
```

## Контракты

```go
// internal/translator/conversation.go
type GenerationCommand int
const (
    CommandAnswer        GenerationCommand = iota // F1
    CommandThinkDeeper                            // F2
    CommandMoreContext                            // F3
    CommandSimplerEnglish                         // F4
)

type ConversationTurn struct {
    Question string
    Answer   string
}

type AnswerRequest struct {
    Question            string           // текущий вопрос
    CandidateContext    string           // постоянные факты кандидата (база CV)
    ConversationContext string           // история интервью (ограниченная)
    Command             GenerationCommand
}

type ConversationHistory struct { … }
func NewConversationHistory(recentTurns, maxContextTokens int) *ConversationHistory
func (h *ConversationHistory) RecordAnswer(question, answer string)
func (h *ConversationHistory) Recent() []ConversationTurn
func (h *ConversationHistory) BuildContext() string

// internal/translator/provider.go
type LLMProvider interface {
    GenerateAnswers(ctx context.Context, req AnswerRequest) ([]string, error)
}

// internal/dispatcher/dispatcher.go
type AnswerGenerator interface {
    GenerateAnswers(ctx context.Context, req translator.AnswerRequest) ([]string, error)
}
```

## Два контекста (строго разделены)

| Контекст | Содержание | Куда попадает | Источник |
|---|---|---|---|
| **Candidate Context** | Постоянные факты кандидата: опыт, проекты, технологии, обязанности, достижения | system prompt | файл `candidate_context.md` (`CANDIDATE_CONTEXT_FILE`, gitignored) |
| **Conversation Context** | Уже обсуждавшееся в текущем интервью (Q/A пары) | user prompt | `ConversationHistory` в памяти |

Правила:

1. LLM не придумывает факты сверх Candidate Context — `SystemPromptAnswerGen` прямо требует «Use only information available in the provided candidate context».
2. Информация о компании/вакансии (зарплата, «у нас 500 сотрудников» и т.п.) остаётся в conversation history как реплика интервьюера и НЕ становится фактом кандидата.
3. Динамическая история НЕ помещается в system prompt — только статичный Candidate Context.
4. Candidate Context имеет приоритет над conversation history.

## Ограничение контекста

```
conversation:
  recent_turns: 6          # максимум turns в context
  max_context_tokens: 4000 # лимит размера (основной критерий)
```

- Основной критерий — **размер текста**, а не число предложений.
- `estimateTokens(s) = (len(s) + 3) / 4` — простая оценка (~1 токен на 4 символа для английского). Заменяется на tokenizer-реализацию текущей модели, когда она станет доступна.
- При превышении лимита: удаляются **старые** turns (с начала), сохраняются самые свежие; текущий вопрос и Candidate Context не удаляются.
- История не растёт бесконечно: `RecordAnswer` держит максимум `recentTurns` turns.

## Regeneration (повторная генерация)

F2–F4 генерируют новую версию ответа на **тот же** вопрос без создания нового turn:

```
Q1 → A1        (F1)
Q1 → A1 → A1'  (F4: A1' заменяет A1)
```

`RecordAnswer` с тем же вопросом (последний turn) заменяет `Answer`, а не добавляет turn. В историю для следующих вопросов попадает только финально выбранная версия (последняя успешная).

## Команды генерации (F1–F4, Esc)

| Клавиша | Команда | Эффект |
|---|---|---|
| F1 | `CommandAnswer` | Обычная генерация ответа на текущий вопрос |
| F2 | `CommandThinkDeeper` | Повторная генерация с глубоким reasoning (не раскрывается, факты не меняются) |
| F3 | `CommandMoreContext` | Больше истории + чуть подробнее (естественная длина) |
| F4 | `CommandSimplerEnglish` | Проще английский, смысл/факты сохранены, RU-перевод соответствует |
| Esc | `Cancel()` | Отмена активной генерации + очистка очереди вопросов |

Реализация:

- `commandCh` (chan `GenerationCommand`) — команды F1–F4.
- `cancelCh` — Esc: `activeCancel()` + флаг `cancelled` + `dropQueue()`.
- Модификаторы команд добавляются в `BuildAnswerPrompt` через `commandInstruction(cmd)`.
- Hotkey: глобальный `RegisterHotKey` (Win32) в `internal/hotkey/` — оверлей `WS_EX_NOACTIVATE` не получает клавиатуру, поэтому Gio `key.Event` не приходит.

## Очередь (последовательная обработка)

- Один `answerWorker` обрабатывает и вопросы, и команды **последовательно** (FIFO) — без параллельных LLM-запросов.
- Причина: Groq free tier возвращает **429** при частых запросах (rate-limit TPM).
- Неблокирующая отправка: при переполнении очереди вопрос дропается с debug-логом.
- Shutdown: `Run` ждёт `<-answerDone` ПЕРЕД `close(answerCh)`; `workerCtx` отменяется при выходе `Run`, worker дренирует очередь.

## Точки расширения → личная база данных (retrieval / RAG)

Текущая архитектура — базис для ответов по личной базе данных:

```
Личная база данных (проекты / знания / документы кандидата)
        │
        ▼  semantic retrieval (будущее)
Relevant Projects / Relevant Context
        │
        ▼
AnswerRequest.CandidateContext      ← добавляется как НОВЫЙ источник фактов
        │
        ▼
Prompt → LLM → answer
```

Что можно добавить, не ломая структуру:

1. **Semantic retrieval** — выбор релевантных проектов/фактов из личной БД → подмешивание в `CandidateContext` (или отдельное поле `RelevantContext`).
2. **Tokenizer-based оценка** — замена `estimateTokens` на точный tokenizer модели.
3. **Conversation summary** — LLM-саммари старой истории вместо простого отбрасывания (сейчас — только последние `recent_turns` turns).
4. **Индексация документов** — личная БД = набор проиндексированных документов (CV, проекты, заметки), по которым ищется контекст под вопрос.

Принцип: retrieval — это **источник** Candidate Context, а не новый pipeline. Вопрос → IsQuestion → (retrieval →) AnswerRequest → Prompt → LLM.
