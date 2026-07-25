# Потоковая схема LLM — генерация подсказок

**Провайдер:** GLM-4.7-Flash (Z.AI) / GPT-4o-mini (OpenAI)
**Протокол:** OpenAI-compatible Chat Completions API
**Библиотека:** `github.com/sashabaranov/go-openai`
**Стриминг:** SSE (Server-Sent Events), `stream: true`
**Документация Z.AI:** https://docs.z.ai/guides/capabilities/streaming

**Важно (v4):** LLM используется **ТОЛЬКО** для генерации подсказок (`GenerateAnswersStream`). Перевод выполняется Gladia (встроенный, модель `enhanced`). `TranslateStream` удалён.

## Поток данных

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    LLM ANSWER GENERATION PIPELINE                       │
│                                                                         │
│  dispatch                                                               │
│  (final ───▶ runDispatch ───▶ if IsQuestion(text):                     │
│   event)                      go p.generateAnswersAsync(text)           │
│                                          │                               │
│                                          ▼                               │
│                              ┌──────────────────────┐                   │
│                              │ generateAnswersAsync │                   │
│                              │                      │                   │
│                              │ ansCtx (10s timeout) │                   │
│                              │ engine.Generate-     │                   │
│                              │   AnswersStream()    │                   │
│                              └─────────┬────────────┘                   │
│                                        │                                 │
│                                        ▼                                 │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │               GenerateAnswersStream() — ПОТОКОВЫЙ               │   │
│  │                                                                   │   │
│  │  ┌──────────────────┐    ┌──────────────────┐                    │   │
│  │  │ ChatCompletion   │    │ SSE Stream       │                    │   │
│  │  │ Request          │───▶│ (CreateChat-     │                    │   │
│  │  │                  │    │  CompletionStream)│                   │   │
│  │  │ Stream: true     │    │                  │                    │   │
│  │  │ Temp: 0.3        │    │ goroutine:       │                    │   │
│  │  │ System: подсказки│    │  for {           │                    │   │
│  │  │ User: вопрос     │    │   recv() →       │                    │   │
│  │  └──────────────────┘    │   tokenCh <- tok │                    │   │
│  │                          │  }               │                    │   │
│  │                          │  close(tokenCh)  │                    │   │
│  │                          └────────┬─────────┘                    │   │
│  │                                   │                              │   │
│  │                          tokenCh (chan string, buf 64)           │   │
│  └───────────────────────────────────┼──────────────────────────────┘   │
│                                      │                                  │
│                                      ▼                                  │
│                          ┌─────────────────────┐                       │
│                          │ Сборка полного текста│                       │
│                          │                     │                       │
│                          │ for token := range  │                       │
│                          │   tokenCh {         │                       │
│                          │   fullText += token │                       │
│                          │ }                   │                       │
│                          └─────────┬───────────┘                       │
│                                    │                                    │
│                                    ▼                                    │
│                          ┌─────────────────────┐                       │
│                          │ parseAnswerHints()  │                       │
│                          │                     │                       │
│                          │ Разбор сырого текста│                       │
│                          │ на 2-3 подсказки    │                       │
│                          │ Формат: "- EN: ...  │                       │
│                          │   RU: ..."          │                       │
│                          └─────────┬───────────┘                       │
│                                    │                                    │
│                                    ▼                                    │
│                          ┌─────────────────────┐                       │
│                          │ UI: Answer-         │                       │
│                          │ Candidates          │                       │
│                          │ (нижняя зона)       │                       │
│                          └─────────────────────┘                       │
└─────────────────────────────────────────────────────────────────────────┘
```

## Статус потоковости

| Компонент | Потоковый? | Метод |
|---|---|---|
| **STT Gladia** | ✅ Да | WebSocket, события real-time |
| **Перевод** | ✅ Да | Gladia (встроенный, модель `enhanced`) |
| **Подсказки LLM** | ✅ Да | `GenerateAnswersStream()` → SSE → `<-chan string` |

## Как определяется вопрос

**Функция:** `translator.IsQuestion(text string) bool` (`engine.go:119`)

Два эвристических правила:

1. **Вопросительный знак:** `strings.Contains(trimmed, "?")`
2. **Вопросительные слова в начале:**
   ```
   what, how, why, when, where, who, which,
   can you, could you, would you, will you, do you,
   have you, did you, are you, is it, is there,
   explain, describe, tell me, elaborate, clarify,
   share, walk me, talk about, give me
   ```

Срабатывает в `runDispatch` при получении финального транскрипта (pipeline.go:507).

## Что происходит при вопросе

```
IsQuestion == true
       │
       ▼
go p.generateAnswersAsync(question)         // АСИНХРОННО, не блокирует dispatch
       │
       ▼
ansCtx, ansCancel := context.WithTimeout(context.Background(), AnswerTimeout)  // 10s
tokenCh, err := engine.GenerateAnswersStream(ansCtx, question)                   // SSE поток
       │
       ▼
for token := range tokenCh { fullText += token }   // собираем токены
       │
       ▼
answers := parseAnswerHints(fullText.String())    // парсим 2-3 подсказки
       │
       ├─ len(answers) == 0  → выход
       │
       └─ len(answers) > 0 → UI.AddMessage(AnswerCandidates)
                              slog.Info("подсказки сгенерированы", ...)
```

## Таймауты

| Операция | Таймаут | Контекст |
|---|---|---|
| Генерация подсказок | `AnswerTimeout` (10s) | `context.WithTimeout(context.Background(), ...)` |

Используется `context.Background()` — не привязан к lifecycle основного контекста (не обрывается при Ctrl+C до истечения таймаута).

## Соответствие спецификации Z.AI SSE

Модель GLM-4.7-Flash (и все GLM 4.5+) нативно поддерживает инкрементальную генерацию через `stream: true`.

| Поле SSE | Наш код | Назначение |
|---|---|---|
| `choices[0].delta.content` | `stream.Recv()` → `delta.Content` | Инкрементальный токен подсказки |
| `choices[0].finish_reason` | `recvErr.Error() == "EOF"` | Конец стрима |
| `usage` | не используется | Статистика токенов (только в последнем чанке) |

**Реализация в `openai.go`:**

- `GenerateAnswersStream` (строка 293) — `CreateChatCompletionStream` + SSE-цикл → `<-chan string`
- `thinkingTransport` (строка 34) инжектит `"thinking":{"type":"disabled"}` только для Z.AI — это единственная Z.AI-специфичная настройка.
