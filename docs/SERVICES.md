---

## Gladia Live API v2

| Параметр | Значение |
|---|---|
| Endpoint init | `POST https://api.gladia.io/v2/live` |
| Endpoint WS | динамический URL (из ответа init) |
| Модель STT | `solaria-1` |
| Модель перевода | `enhanced` |
| Кодирование | `wav/pcm`, 16-bit, 16000 Hz, mono |
| Endpointing | `0.3` |
| Аутентификация | `x-gladia-key` header |

**Двухфазный коннект:**
1. `POST /v2/live` с JSON-конфигурацией (модель, язык, перевод, endpointing)
2. Ответ: `{"id": "...", "url": "wss://..."}` — статус 201 Created
3. WebSocket dial по полученному URL
4. Отправка PCM-фреймов (BinaryMessage) + чтение JSON-событий

**События Gladia:**

| Тип | STTEvent.Event | ChannelID | Назначение |
|---|---|---|---|
| `transcript` (is_final=false) | `EventUpdate` | `"speaker"` | Interim → UI |
| `transcript` (is_final=true) | `EventEndOfTurn` | `"speaker"` | Final → dispatch (сохраняется как lastOriginal) |
| `translation` | `EventEndOfTurn` | `"translation"` | Перевод → UI Translation + связка с lastOriginal |

**Связывание transcript ↔ translation:** Gladia шлёт `translation` ПОСЛЕ `transcript`. Dispatch хранит `lastOriginal` — последний финальный транскрипт. При получении `translation` выводит пару (оригинал + перевод) в историю.

---

## LLM: GLM-4.7-Flash (Z.AI)

| Параметр | Значение |
|---|---|
| Endpoint | `https://api.z.ai/api/paas/v4/chat/completions` |
| Модель | `glm-4.7-flash` |
| Вызов | `GenerateAnswers` (синхронный) + `GenerateAnswersStream` (SSE-стриминг) |
| max_tokens | `LLM_MAX_TOKENS` (default 1024) |
| temperature | 0.3 (answers) |
| Подсказок | **1** (промпт `SystemPromptAnswerGen` → "EXACTLY 1 bullet point") |
| Формат | `EN: <English> \| RU: <Russian>` — UI разделяет на две строки |
| Контекст | `CVContext` из `config.yaml` → `Pipeline.Config.CVContext` → `engine.SetCVContext()` → `BuildAnswerPrompt(question, cvContext)` |
| Thinking | **включён по умолчанию** (GLM-4.7-Flash включает thinking автоматически). `DisableThinking()` посылает `"thinking": {"type": "disabled"}` в raw HTTP-запросе |

**LLM используется ТОЛЬКО для генерации подсказок.** Перевод выполняется Gladia (встроенный, модель `enhanced`).

**thinkingTransport удалён из стриминг-пути** — управление thinking теперь через флаг `disableThinking` в `OpenAIProvider`, без инжекции в тело запроса на уровне транспорта.

---
