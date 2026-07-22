# Translator — AI-ассистент собеседований и встреч

Оверлей реального времени для Windows. Переводит речь собеседника (EN→RU),
сохраняет IT-термины, генерирует подсказки к ответам при детекции вопроса.

## Возможности

- **Двухканальный захват аудио** — VB-Cable (собеседник) + микрофон
- **Распознавание речи** — Deepgram WebSocket /v2/listen (Flux), задел под локальный Sherpa-onnx
- **Перевод** — GPT-4o-mini с сохранением IT-терминов (Deadlock, Kubernetes, CQRS…)
- **Подсказки** — 2–3 тезиса ответа при детекции вопроса
- **Прозрачный оверлей** — GioUI, всегда поверх окон, не перехватывает фокус
- **Логирование** — JSON-транскрипты + PCM-дампы аудио

## Требования

- **Go 1.22+**
- **GCC (MinGW-w64)** — обязателен для production-сборки, путь: MSYS2 `C:\msys64\ucrt64\bin`
- **VB-Cable** ([vb-audio.com/Cable](https://vb-audio.com/Cable/)) — виртуальный аудиокабель
- **API-ключи:** Deepgram (STT) + OpenAI (перевод)

## Быстрый старт

```bash
git clone https://github.com/MasterErik/translator.git
cd translator
```

Создать `.env` с ключами (см. раздел Конфигурация).

## Сборка

### Production (полная, с реальным аудио)

**Требуется GCC в PATH.** Без GCC получится сборка-заглушка — аудио не пишется (см. раздел «Устранение неполадок»).

```bash
# Убедиться что GCC доступен
export PATH="/c/msys64/ucrt64/bin:$PATH"

# Полная сборка
go build -o translator.exe ./cmd/app
```

Результат: `translator.exe` в корне проекта.

### Для разработки и тестов (без GCC)

Если GCC недоступен, сборка использует заглушку захвата аудио (тишина вместо реального звука).
STT, перевод и GioUI при этом работают — можно тестировать логику без аудиожелеза.

```bash
# Явно отключить CGO
CGO_ENABLED=0 go build -o translator.exe ./cmd/app
```

## Запуск

```bash
# Собранным .exe
./translator.exe

# Или напрямую
go run ./cmd/app
```

Остановка: `Ctrl+C` — корректное завершение, логи сохраняются в `logs/`.

Логи выводятся в stderr в JSON-формате (slog). Пример нормального запуска:

```json
{"msg":"translator запускается","deepgram_model":"flux-general-en","openai_model":"gpt-4o-mini"}
{"msg":"доступные аудиоустройства","loopback":["CABLE Input ...",...],"capture":[...]}
{"msg":"создание захвата аудио","loopback_device":"CABLE Input (VB-Audio Virtual Cable)","mic_device":"<системный по умолчанию>"}
{"msg":"translator работает, Ctrl+C для остановки"}
```

## Конфигурация

### API-ключи (`.env`)

```env
DEEPGRAM_API_KEY=ваш_deepgram_ключ
OPENAI_API_KEY=ваш_openai_ключ
```

### Настройки аудио

Имена устройств задаются в `.env`, `config.yaml` или переменных окружения.
Приоритет: **env-переменные > `.env` > `config.yaml` > системные по умолчанию**.

**.env (рекомендуется для translator.exe):**

```env
LOOPBACK_DEVICE=CABLE Input (VB-Audio Virtual Cable)
MIC_DEVICE=Microphone (Realtek High Definition Audio)
```

**config.yaml:**

```yaml
loopback_device: "CABLE Input (VB-Audio Virtual Cable)"
mic_device:     "Microphone (Realtek High Definition Audio)"
```

Если поля пусты — используются системные устройства по умолчанию (звук из динамиков).

### Как узнать имена устройств

**Способ 1 — встроенный cable_test (рекомендуется):**

```bash
go run ./cmd/cable_test
```

Выводит все loopback/capture устройства с пометкой ★ для устройств VB-Cable
и делает пробный 5-секундный захват.

**Способ 2 — PowerShell:**

```powershell
Install-Module -Name AudioDeviceCmdlets -Scope CurrentUser
Get-AudioDevice -List          # все устройства
Get-AudioDevice -Playback      # воспроизведение → для LOOPBACK_DEVICE
Get-AudioDevice -Recording     # запись → для MIC_DEVICE
```

**Способ 3 — настройки Windows:**
Параметры → Звук → Управление звуковыми устройствами.

## Настройка аудиоканалов

Приложение захватывает два аудиопотока одновременно:

| Канал | Тип WASAPI | Устройство | Назначение |
|-------|-----------|-----------|------------|
| **Loopback** | Playback | `CABLE Input` (VB-Cable) | Речь собеседника → STT → перевод |
| **Микрофон** | Recording | Системный микрофон | Ваш голос → логирование |

### Как работает VB-Cable

```
Chrome/Teams → CABLE Input (Playback) → WASAPI Loopback → Translator
Микрофон     → WASAPI Capture          → Translator
```

1. Установите VB-Cable с [vb-audio.com/Cable](https://vb-audio.com/Cable/)
2. В настройках звука приложения для звонков (Zoom, Teams, Meet) выберите `CABLE Input` как устройство вывода
3. В `.env` или `config.yaml` укажите:
   - `LOOPBACK_DEVICE=CABLE Input (VB-Audio Virtual Cable)`
   - `MIC_DEVICE` — ваш физический микрофон
4. Приложение читает звук из CABLE Input (loopback) и микрофона, преобразует 48kHz Stereo → 16kHz Mono, отправляет в Deepgram

### Без VB-Cable

Оставьте `LOOPBACK_DEVICE` пустым — захватывается системный звук по умолчанию.
Подходит для тестов, но **смешивает ваш голос с голосом собеседника**
(двухканальное разделение без VB-Cable невозможно).

## Устранение неполадок

### «translator запускается (stub-захват, без CGO)»

Сборка прошла без GCC — захват аудио работает в режиме заглушки (тишина).
Реальный звук не пишется, устройства не перечисляются.

**Решение:** добавить GCC в PATH перед сборкой:

```bash
export PATH="/c/msys64/ucrt64/bin:$PATH"
go build -o translator.exe ./cmd/app
```

### «C compiler "gcc" not found»

GCC не установлен или не в PATH. Установить через MSYS2:

```bash
pacman -S mingw-w64-ucrt-x86_64-gcc
```

### «устройство не найдено среди loopback-устройств»

Проверьте, что в `LOOPBACK_DEVICE` указано **Playback**-устройство (не Recording).
`CABLE Input` — Playback, `CABLE Output` — Recording. Для loopback нужно `CABLE Input`.

Запустите `go run ./cmd/cable_test` чтобы увидеть полный список.

### go test -race не работает

Race detector требует CGO: `export PATH="/c/msys64/ucrt64/bin:$PATH"`.

## Тесты

```bash
export PATH="/c/msys64/ucrt64/bin:$PATH"

go test ./... -count=1        # все тесты
go test -race ./...           # детектор гонок
go vet ./...                  # статический анализ
```

## Ручное тестирование STT и перевода

```bash
python generate_test_wav.py         # сгенерировать тестовую речь (Windows TTS)
go run ./cmd/manual_test            # проверить STT + перевод на реальных API
```

## Проверка VB-Cable

```bash
go run ./cmd/cable_test             # список устройств + пробный захват
```

## Структура проекта

```
cmd/
  app/              точка входа + graceful shutdown
  cable_test/       проверка VB-Cable (перечисление устройств + пробный захват)
  manual_test/      ручной тест STT + перевода (Deepgram + OpenAI)
internal/
  common/           Config, STTEvent, UIEvent
  capture/          malgo: loopback + микрофон, ресамплер 48→16kHz Stereo→Mono
  stt/              DeepgramProvider, SherpaOnnxProvider (заглушка)
  translator/       OpenAIProvider, TranslationEngine, промпты
  ui/               GioUI-оверлей, Win32 WS_EX_TOPMOST/LAYERED
  logger/           FileSessionLogger: JSON + PCM-дамп
```

## Документация

- `ARCHITECTURE.md` — полная архитектура, потоки данных, схема VB-Cable
- `AGENTS.md` — стандарты кода (Go, concurrency, тестирование)
- `.hermes/plans/translator.md` — план реализации с отметками о выполнении

## Лицензия

MIT
