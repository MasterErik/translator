# Потоковая схема LLM-переводчика и генерации подсказок

**Провайдер:** GLM-4.7-Flash (Z.AI) / GPT-4o-mini (OpenAI)  
**Протокол:** OpenAI-compatible Chat Completions API  
**Библиотека:** `github.com/sashabaranov/go-openai`  
**Стриминг:** SSE (Server-Sent Events), `stream: true`  
**Документация Z.AI:** https://docs.z.ai/guides/capabilities/streaming

## Поток данных

```
┌─────────────────────────────────────────────────────────────────────────┐
│                        LLM TRANSLATION PIPELINE                        │
│                                                                         │
│  textStream                                                             │
│  (final ───▶ runDispatch ───▶ go handleStreamingTranslation(event)     │
│   event)                           │                                    │
│                                    ▼                                    │
│                          ┌─────────────────────┐                       │
│                          │ UI: [переводится...] │  ← pending (янтарный) │
│                          └─────────┬───────────┘                       │
│                                    │                                    │
│                                    ▼                                    │
│                          ┌─────────────────────┐                       │
│                          │ ProcessFinal-       │                       │
│                          │ TranscriptStream()  │                       │
│                          │                     │                       │
│                          │ 1. Добавить в окно  │                       │
│                          │ 2. Взять историю    │                       │
│                          │ 3. Вызвать          │                       │
│                          │   TranslateStream() │                       │
│                          └─────────┬───────────┘                       │
│                                    │                                    │
│                                    ▼                                    │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │                     TranslateStream() — ПОТОКОВЫЙ                │   │
│  │                                                                   │   │
│  │  ┌──────────────────┐    ┌──────────────────┐                    │   │
│  │  │ ChatCompletion   │    │ SSE Stream       │                    │   │
│  │  │ Request          │───▶│ (CreateChat-     │                    │   │
│  │  │                  │    │  CompletionStream)│                   │   │
│  │  │ Stream: true     │    │                  │                    │   │
│  │  │ Temp: 0.1        │    │ goroutine:       │                    │   │
│  │  │ System: перевод  │    │  for {           │                    │   │
│  │  │ User: текст+ист. │    │   recv() →       │                    │   │
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
│                          │ ИНКРЕМЕНТАЛЬНЫЙ UI  │                       │
│                          │                     │                       │
│                          │ for token := range  │                       │
│                          │   tokenCh {         │                       │
│                          │   fullText += token │                       │
│                          │   UI.AddMessage(    │                       │
│                          │     streaming,      │  ← зелёный фон        │
│                          │     fullText.String │                       │
│                          │   )                 │                       │
│                          │ }                   │                       │
│                          └─────────┬───────────┘                       │
│                                    │                                    │
│                                    ▼                                    │
│                          ┌─────────────────────┐                       │
│                          │ UI: done (тёмный)   │                       │
│                          └─────────┬───────────┘                       │
│                                    │                                    │
│                                    ▼                                    │
│                          ┌─────────────────────┐                       │
│                          │ IsQuestion(text) ?  │                       │
│                          └─────────┬───────────┘                       │
│                                    │                                    │
│                         ┌──────────┴──────────┐                        │
│                         │ YES                 │ NO                     │
│                         ▼                     ▼                        │
│              ┌──────────────────┐    ┌──────────────┐                 │
│              │ GenerateAnswers  │    │ Конец        │                 │
│              │ (БЛОКИРУЮЩИЙ ⚠)  │    └──────────────┘                 │
│              │                  │                                      │
│              │ System: подсказки│                                      │
│              │ User: вопрос + CV│                                      │
│              │ Temp: 0.3        │                                      │
│              │ Stream: false    │  ← НЕ потоковый!                     │
│              │                  │                                      │
│              │ → ждём полный    │                                      │
│              │   ответ          │                                      │
│              │ → парсим 2-3     │                                      │
│              │   подсказки      │                                      │
│              └────────┬─────────┘                                      │
│                       ▼                                                │
│              ┌──────────────────┐                                      │
│              │ UI: Answer-      │                                      │
│              │ Candidates       │                                      │
│              │ (нижняя зона)    │                                      │
│              └──────────────────┘                                      │
└─────────────────────────────────────────────────────────────────────────┘
```

## Статус потоковости

| Компонент | Потоковый? | Метод |
|-----------|-----------|-------|
| **STT Deepgram** | ✅ Да | WebSocket, события real-time |
| **Перевод LLM** | ✅ Да | `TranslateStream()` → SSE → `<-chan string` |
| **Подсказки LLM** | ✅ Да | `GenerateAnswersStream()` → SSE → `<-chan string` (асинхронно в `go func()`) |

## Как определяется вопрос

**Функция:** `translator.IsQuestion(text string) bool` (`engine.go:210`)

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

Срабатывает **после** завершения стриминг-перевода (pipeline.go:496).

## Что происходит при вопросе

```
IsQuestion == true
       │
       ▼
go p.generateAnswersAsync(event, translation)   // АСИНХРОННО, не блокирует перевод
       │
       ▼
ansCtx, _ := context.WithTimeout(context.Background(), AnswerTimeout)  // 10s
tokenCh, err := engine.GenerateAnswersStream(ansCtx, event.Text)       // SSE поток
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
                              sessLog.LogTranslation(..., answers)
```

## Таймауты

| Операция | Таймаут | Контекст |
|----------|---------|----------|
| Стриминг-перевод | `StreamTimeout` (15s) | `context.WithTimeout(context.Background(), ...)` |
| Генерация подсказок | `AnswerTimeout` (10s) | `context.WithTimeout(context.Background(), ...)` |

Оба используют `context.Background()` — не привязаны к lifecycle основного контекста (не обрываются при Ctrl+C до истечения таймаута).

## Скользящее окно истории

`TranslationEngine` держит `maxWindow` (default 5) последних фраз.  
История = все фразы кроме последней (текущей).  
При переводе история передаётся в промпт для контекста:

```
[History]
- предыдущая фраза 1
- предыдущая фраза 2
...

[Translate]
текущая фраза
```

## Соответствие спецификации Z.AI SSE

Модель GLM-4.7-Flash (и все GLM 4.5+) нативно поддерживает инкрементальную
генерацию через `stream: true`. Модель сама отдаёт правильный перевод токен
за токеном — никакой пост-обработки не требуется.

| Поле SSE | Наш код | Назначение |
|----------|---------|------------|
| `choices[0].delta.content` | `stream.Recv()` → `delta.Content` | Инкрементальный токен перевода/подсказки |
| `choices[0].finish_reason` | `recvErr.Error() == "EOF"` | Конец стрима |
| `usage` | не используется | Статистика токенов (только в последнем чанке) |

**Реализация в `openai.go`:**

- `TranslateStream` (строка 223) — `CreateChatCompletionStream` + SSE-цикл → `<-chan string`
- `GenerateAnswersStream` (строка 293) — аналогично для подсказок

Обе функции используют `go-openai` который автоматически парсит SSE-фреймы.
Мы не работаем с raw SSE — библиотека скрывает транспортный уровень.
`thinkingTransport` (строка 34) инжектит `"thinking":{"type":"disabled"}`
только для Z.AI — это единственная Z.AI-специфичная настройка.

