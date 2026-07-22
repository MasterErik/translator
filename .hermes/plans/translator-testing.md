# План тестирования и доработок Translator v2

**Создан:** 2026-07-22 · **Обновлён:** 2026-07-22 (beep, .env, многопоточная схема)
**Контекст:** завершены этапы 0–8 базового плана, 92 теста pass, data race: 0

---

## Выявленные проблемы

### П1. Синхронный перевод блокирует поток STT-событий (КРИТИЧЕСКАЯ)

**Корень:** `engine.ProcessFinalTranscript()` в `app.go:124` вызывается синхронно. GPT-4o-mini API — 1–3с блокировки. Новые STT-события не обрабатываются.

**Решение:** Worker pool (N=3 горутины) + канал результатов. STT-dispatch читает из `textStream` и `transResults` параллельно через `select`.

### П2. Сохранение звука — только raw PCM, нет MP3

**Решение:** 
- `beep` (MIT) — буферизация PCM, `beep.Buffer`
- `beep/wav` (MIT) — WAV-кодирование (запасной/основной)
- `go-lame` (MIT) — MP3-кодирование через CGo/libmp3lame
- При недоступности libmp3lame: fallback на WAV

### П3. Сохранение не опционально

**Решение:** `SAVE_AUDIO=true/false` в `.env` (приоритет над config.yaml). По умолчанию `false`.

### П4. Корректный flush при shutdown

Уже реализовано в `Close()`, требуется интеграционный тест.

---

## Анализ библиотек

### beep (github.com/gopxl/beep) — MIT

| Возможность | Статус | Применение |
|---|---|---|
| `beep.Streamer` | ✅ | Интерфейс audio-потока |
| `beep.Buffer` | ✅ | Буферизация PCM в памяти |
| `beep.Resampler` | ✅ | Замена `resampler.go` |
| `beep.Mixer` | ✅ | Слияние speaker+mic |
| `beep/wav` encode | ✅ | Сохранение в WAV |
| `beep/mp3` encode | ❌ | Только decode |

### go-lame (github.com/sunicy/go-lame) — MIT

| Возможность | Статус |
|---|---|
| PCM → MP3 | ✅ (CGo, libmp3lame) |
| WAV → MP3 | ✅ |
| Качество | 0-9 (рекомендуется 5) |

**Итог:** beep для аудио-пайплайна, go-lame для MP3. Если go-lame не соберётся — fallback на beep/wav.

---

## Многопоточная архитектура (см. схему выше)

### Гор 's (10 базовых + N workers)

1. `main` — graceful shutdown
2. `capture·loopback` — malgo WASAPI loopback
3. `capture·mic` — malgo WASAPI microphone
4. `route/merge` — слияние каналов → AudioStream
5. `stt·deepgram` — WebSocket send/recv
6. **`stt·dispatch`** — центральный узел: select{textStream, transResults}
7. `ui·gioui` — рендеринг
8. `audio·saver` — (опционально) beep.Buffer → WAV/MP3
9. `worker·1..3` — параллельные переводчики
10. `logger` — async JSON (как сейчас)

### Каналы

| Канал | Тип | Буфер | Путь |
|---|---|---|---|
| speakerPCM | chan []byte | 32 | loopback → route |
| micPCM | chan []byte | 32 | mic → route |
| audioStream | chan []byte | 64 | route → deepgram |
| textStream | chan STTEvent | 16 | deepgram → dispatch |
| transResults | chan transJob | 16 | workers → dispatch |
| logJobs | chan logJob | 256 | dispatch → logger |

### Shutdown-порядок

1. ctx cancel → capture.Close() → speakerPCM/micPCM closed
2. route завершается
3. deepgram.Stop() → textStream closed
4. workers завершаются (ctx в Translate)
5. transResults closed → dispatch завершается
6. ui получает DestroyEvent
7. audio·saver flush + close
8. logger.Close() → drain + flush
9. os.Exit(0)

---

## Этап 9: Асинхронный перевод (worker pool)

**Субагент 1 — deleg_async_translate**

**Файлы:**
- `cmd/app/dispatch.go` (новый) — STT-dispatch: центральный select-узел
- `cmd/app/worker.go` (новый) — worker pool: N горутин перевода
- `cmd/app/app.go` — удаление синхронного вызова из `runSTT`
- `cmd/app/main.go` — wire-up worker pool

**Шаги:**
1. `transJob{event, resultCh}` — структура задания перевода
2. `startWorkers(ctx, engine, jobs chan transJob, N int) chan transJob` — запуск N воркеров
3. `runDispatch(ctx, textStream, overlay, sessLog, engine, N)` — центральный цикл
4. Интеграция в `main.go`

**Критерии:**
- Interim не блокируются переводом
- 3 перевода могут идти параллельно
- `go test -race ./...` — чисто
- Нет утечек горутин

---

## Этап 10: Сохранение аудио (beep + MP3/WAV)

**Субагент 2 — deleg_audio_save**

**Файлы:**
- `internal/logger/audio.go` (новый) — AudioSaver: beep.Buffer + WAV/MP3
- `internal/logger/session.go` — интеграция AudioSaver
- `internal/logger/mp3.go` (новый) — MP3-кодирование через go-lame
- `internal/logger/audio_test.go` (новый)

**Шаги:**
1. `go get github.com/gopxl/beep/v2`
2. `go get github.com/sunicy/go-lame`
3. `AudioSaver` struct: `beep.Buffer` + канал PCM → фоновый flush
4. WAV: `beep/wav.Encode()` при flush/close
5. MP3: `go-lame` writer при наличии libmp3lame, иначе fallback WAV
6. Интеграция в `FileSessionLogger`

**Критерии:**
- Валидный WAV/MP3 после shutdown
- Размер MP3 < 20% raw PCM
- Конкуррентная запись speaker+mic → два файла
- `go build` с CGO и без (stub)

---

## Этап 11: Опциональное сохранение (.env)

**Субагент 3 — deleg_save_config**

**Файлы:**
- `internal/common/config.go` — `SaveAudio bool` из `SAVE_AUDIO`
- `.env` — `SAVE_AUDIO=false`
- `config.yaml` — `save_audio: false`
- `cmd/app/app.go` — проверка `cfg.SaveAudio`

**Шаги:**
1. `Config.SaveAudio` — из `SAVE_AUDIO` env (true/1/yes → true, по умолчанию false)
2. Приоритет: env > .env > yaml
3. `routeAudioChannel` — параметр `saveAudio bool`
4. `main.go` — передача `cfg.SaveAudio` в `runCapture`

**Критерии:**
- `SAVE_AUDIO=false` → аудио не пишется, `audio/` не создаётся
- `SAVE_AUDIO=true` → аудио пишется
- Тесты: env override, default false

---

## Этап 12: Интеграция

**После завершения 9+10+11**

- Полный интеграционный тест
- `go test -race -count=10 ./...`
- Проверка утечек горутин
- Стресс-тест: 100 транскриптов подряд

---

## Порядок запуска субагентов

```
Этап 9  (async translate) ──┐
                             ├── параллельно
Этап 11 (save config)    ──┘

        │ (после завершения)
        ▼
Этап 10 (audio save: beep + MP3/WAV)

        │
        ▼
Этап 12 (интеграция)
```

Этапы 9 и 11 — независимы, запускаются параллельно.
Этап 10 — после 11 (меняет logger).
Этап 12 — после всех.
