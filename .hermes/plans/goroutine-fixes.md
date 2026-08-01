# План: Исправление гонок горутин и аудит тестов

## Часть 1 — Исправление критических проблем

### P1: Двойное закрытие wsConn (гонка writePump/readPump)

**Файл:** `internal/stt/gladia.go:276-355`

**Проблема:** Обе горутины `writePump` и `readPump` в defer закрывают `wsConn`. При shutdown:
`Stop()` делает `g.cancel()` + `wsConn.Close()` → writePump просыпается по `ctx.Done()`, видит `wsConn == nil` (уже занулён Stop'ом) — OK. Но если writePump падает по ошибке записи ДО Stop(), он закрывает wsConn. Затем readPump тоже падает и пытается закрыть уже закрытый wsConn → `gorilla/websocket` может запаниковать на двойном `Close()`.

**Исправление:**

1. Добавить `wsClosed atomic.Bool` в `GladiaProvider`
2. В defer'ах обеих горутин — проверять и устанавливать флаг атомарно:
```go
if !g.wsClosed.CompareAndSwap(false, true) {
    return // уже закрыто другой горутиной
}
g.mu.Lock()
if g.wsConn != nil {
    _ = g.wsConn.Close()
    g.wsConn = nil
}
g.mu.Unlock()
```
3. В `Stop()` — тоже через CAS: `if !g.wsClosed.CompareAndSwap(false, true) { return nil }`

**Тест:** `TestGladiaProvider_DoubleCloseWsConn` — симулировать отказ writePump (закрыть audioCh) и проверить что readPump не паникует при выходе.

---

### P2: LogTranslation в горутинах переживает shutdown

**Файл:** `internal/dispatcher/dispatcher.go:167-179`

**Проблема:** `go func() { sessLog.LogTranslation(...) }()` запускается без ожидания. При shutdown `sessLog.Close()` вызывается до завершения этих горутин → паника/потеря логов.

**Исправление:**

Добавить `sync.WaitGroup logWg` в `Dispatcher`. В `route()` — `d.logWg.Add(1)` перед `go func()`, `defer d.logWg.Done()` внутри. В `Run()` — после выхода из цикла чтения textStream: `d.logWg.Wait()` перед сигналом `dispatchDone`.

```go
// В Run():
defer func() {
    d.logWg.Wait()  // ← ждём все асинхронные логи
    if d.answerCh != nil {
        <-d.answerDone
        close(d.answerCh)
    }
    if done != nil {
        select {
        case done <- struct{}{}:
        default:
        }
    }
}()
```

**Тест:** `TestDispatcher_LogTranslationSurvivesShutdown` — отправить translation-событие, дождаться dispatchDone, проверить что `LogTranslation` был вызван до `sessLog.Close()` (через spy-логгер).

---

### P3: GladiaProvider.Stop() не ждёт завершения горутин

**Файл:** `internal/stt/gladia.go:183-199`

**Проблема:** `Stop()` отменяет контекст и закрывает wsConn, но не ждёт writePump/readPump. Pipeline сразу переходит к `<-dispatchDone`, а gladia-горутины могут ещё писать в `sessLog`.

**Исправление:**

Добавить `sync.WaitGroup pumpWg` в `GladiaProvider`. В `Start()`:
```go
g.pumpWg.Add(2)
go func() { defer g.pumpWg.Done(); g.writePump() }()
go func() { defer g.pumpWg.Done(); g.readPump() }()
```
В `Stop()`: `g.pumpWg.Wait()` после `g.cancel()`.

**Тест:** `TestGladiaProvider_StopWaitsForPumps` — эмуляция через mockWebSocket: вызвать Stop, проверить что writePump/readPump завершились до возврата Stop.

---

### P4: drainAudio deadlock в эмуляторе

**Файл:** `internal/stt/emulator.go:158-174`

**Проблема:** При пустом `audioCh` (отправитель прекратил отправку) `drainAudio` блокируется на `case chunk := <-audioCh`, не реагируя на `eventsDone` (который ещё не закрыт).

**Исправление:** Добавить тайм-аут в select:
```go
case <-time.After(200 * time.Millisecond):
    // audioCh пуст, перепроверяем eventsDone в следующей итерации
```

**Альтернатива (лучше):** закрывать `audioCh` извне после отправки всех чанков. Но `audioCh` принадлежит эмулятору. Решение: эмулятор должен сам закрыть audioCh при Stop(). `Stop()` вызывает `g.cancel()` → `run()` ловит `ctx.Done()` и делает `close(g.textCh)`, но audioCh остаётся открытым. Добавить `close(g.audioCh)` в run() при ctx.Done().

**Тест:** `TestGladiaEmulator_DrainAudioTimeout` — отправить ровно minAudioBytes, не закрывать audioCh, проверить что textCh закрывается (sendEvents завершается).

---

## Часть 2 — Аудит и правка тестов

### Избыточные/слабые тесты

| Тест | Файл | Проблема | Действие |
|---|---|---|---|
| `TestSaveAudioChunk_MultipleChannels` | `logger/session_test.go:337` | Ищет ОДИН файл `session_` для двух каналов, но аудио пишется в один общий файл — тест ничего не проверяет | **Удалить** |
| `TestMP3FileSize` | `logger/session_test.go:449` | Дублирует `TestMP3Encoding` (оба тестируют кодирование аудио) | **Объединить** с `TestMP3Encoding`, проверять и размер, и наличие |
| `TestAudioConcurrentWrites` | `logger/session_test.go:497` | Проверяет только что файл не пустой, не проверяет целостность | **Усилить:** добавить проверку количества записанных фреймов |
| `TestAudioGracefulClose` | `logger/session_test.go:538` | `time.Sleep(10ms)` — flaky, зависит от тайминга | **Переписать:** использовать канал для синхронизации вместо sleep |

### Отсутствующие тесты (добавить)

| Тест | Пакет | Что проверяет |
|---|---|---|
| `TestGladiaProvider_DoubleCloseWsConn` | `stt` | CAS-защита от двойного Close (P1) |
| `TestGladiaProvider_StopWaitsForPumps` | `stt` | Stop() ждёт writePump/readPump (P3) |
| `TestDispatcher_LogTranslationSurvivesShutdown` | `dispatcher` | Асинхронные логи завершаются до dispatchDone (P2) |
| `TestGladiaEmulator_DrainAudioTimeout` | `stt` | drainAudio не виснет при пустом audioCh (P4) |
| `TestPipeline_ShutdownGoroutineLeak` | `pipeline` | Проверка `runtime.NumGoroutine()` до/после shutdown |

---

## Порядок реализации

### Этап 1: GladiaProvider (P1 + P3) — `internal/stt/gladia.go`
1. Добавить `wsClosed atomic.Bool` + `pumpWg sync.WaitGroup`
2. `Start()`: `pumpWg.Add(2)` перед запуском горутин
3. `Stop()`: CAS на `wsClosed` + `pumpWg.Wait()`
4. `writePump`/`readPump`: CAS на `wsClosed` в defer
5. Тест: `TestGladiaProvider_DoubleCloseWsConn` + `TestGladiaProvider_StopWaitsForPumps`

### Этап 2: Dispatcher (P2) — `internal/dispatcher/dispatcher.go`
1. Добавить `logWg sync.WaitGroup` в `Dispatcher`
2. `route()`: `d.logWg.Add(1)` / `defer d.logWg.Done()`
3. `Run()`: `d.logWg.Wait()` перед `dispatchDone`
4. Тест: `TestDispatcher_LogTranslationSurvivesShutdown`

### Этап 3: Эмулятор (P4) — `internal/stt/emulator.go`
1. `run()`: `defer close(g.audioCh)` при `ctx.Done()`
2. `drainAudio`: добавить `case <-time.After(200ms):` или убрать блокирующее чтение
3. Тест: `TestGladiaEmulator_DrainAudioTimeout`

### Этап 4: Аудит тестов
1. Удалить `TestSaveAudioChunk_MultipleChannels`
2. Объединить `TestMP3FileSize` с `TestMP3Encoding`
3. Усилить `TestAudioConcurrentWrites`
4. Переписать `TestAudioGracefulClose` без sleep
5. Добавить `TestPipeline_ShutdownGoroutineLeak`

### Этап 5: Верификация
```bash
go vet ./internal/...
go test -short ./internal/...
go test -count=1 -race ./internal/stt/... ./internal/dispatcher/...  # если TSan доступен
```
