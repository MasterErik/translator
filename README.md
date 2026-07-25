# Translator — AI-ассистент собеседований и встреч

Оверлей реального времени для Windows. Распознаёт речь собеседника, переводит EN→RU (Gladia Translation), сохраняет IT-термины, генерирует подсказки к ответам при детекции вопроса.

## Возможности

- **Двухканальный захват аудио** — VB-Cable (собеседник) + микрофон
- **Распознавание речи + перевод** — Gladia Solaria-1 (STT) + встроенный перевод (enhanced) через единый WebSocket
- **Стриминг** — Gladia WebSocket для STT + перевода; GLM-4.7-Flash (Z.AI) для подсказок
- **Трёхзонный UI** — текущая речь / история перевода (10 строк) / подсказки
- **Цветовой индикатор** — янтарный (ожидание) → зелёный (стриминг) → тёмный (готово)
- **Подсказки** — 2–3 тезиса ответа при детекции вопроса (LLM, асинхронно)
- **Прозрачный оверлей** — GioUI, всегда поверх окон, не перехватывает фокус
- **Логирование** — CSV-транскрипты + MP3/WAV дампы аудио

## Требования

- **Go 1.22+**
- **GCC (MinGW-w64)** — обязателен для production-сборки, путь: MSYS2 `C:\msys64\ucrt64\bin`
- **VB-Cable** ([vb-audio.com/Cable](https://vb-audio.com/Cable/)) — виртуальный аудиокабель
- **API-ключи:** Gladia (STT + перевод) + Z.AI (подсказки, OpenAI-совместимый)

## Быстрый старт

```bash
git clone https://github.com/MasterErik/translator.git
cd translator
```

Создать `.env` с ключами (см. раздел Конфигурация).

## Сборка

### Production (полная, с реальным аудио)

**Требуется GCC в PATH.** Без GCC получится сборка-заглушка — аудио не пишется.

```bash
export PATH="/c/msys64/ucrt64/bin:$PATH"
go build -o translator.exe .
```

Результат: `translator.exe` в корне проекта.

### Для разработки и тестов (без GCC)

STT, перевод и GioUI работают — можно тестировать логику без аудиожелеза.

```bash
CGO_ENABLED=0 go build -o translator.exe .
```

## Запуск

```bash
./translator.exe
# или
go run .
```

Остановка: `Ctrl+C` или закрытие окна из контекстного меню таскбара.

## Конфигурация

### API-ключи и модель (`.env`)

```env
GLADIA_API_KEY=ваш_gladia_ключ
OPENAI_API_KEY=ваш_zai_ключ
LLM_BASE_URL=https://api.z.ai/api/paas/v4/
OPENAI_MODEL=glm-4.7-flash
LLM_MAX_TOKENS=1024
```

### Настройки аудио

```env
LOOPBACK_DEVICE=CABLE Input (VB-Audio Virtual Cable)
MIC_DEVICE=Microphone (Realtek High Definition Audio)
SAVE_AUDIO=true
```

### Настройки оверлея

```env
OVERLAY_WIDTH=800
OVERLAY_HEIGHT=400
OVERLAY_MAX_LINES=10
```

### config.yaml (несекретные параметры)

```yaml
openai_model: "glm-4.7-flash"
llm_base_url: "https://api.z.ai/api/paas/v4/"
target_lang: "ru"
window_size: 5
save_audio: false
```

Приоритет: **env > .env > config.yaml**

### Как узнать имена аудиоустройств

```bash
go run ./test/cable_test
```

Выводит все loopback/capture устройства с пометкой ★ для VB-Cable.

## Настройка аудиоканалов

| Канал | Тип WASAPI | Устройство | Назначение |
|-------|-----------|-----------|------------|
| **Loopback** | Playback | `CABLE Input` (VB-Cable) | Речь собеседника → STT → перевод |
| **Микрофон** | Recording | Системный микрофон | Ваш голос → логирование |

```
Chrome/Teams → CABLE Input (Playback) → WASAPI Loopback → Translator
Микрофон     → WASAPI Capture          → Translator
```

## Устранение неполадок

### «translator запускается (stub-захват, без CGO)»

Сборка без GCC — аудио не пишется. Добавить GCC в PATH:
```bash
export PATH="/c/msys64/ucrt64/bin:$PATH"
```

### «C compiler "gcc" not found»

```bash
pacman -S mingw-w64-ucrt-x86_64-gcc
```

### «устройство не найдено среди loopback-устройств»

`LOOPBACK_DEVICE` должно быть **Playback**-устройством. VB-Cable: `CABLE Input` = Playback, `CABLE Output` = Recording.

### Нет перевода / пустой перевод

Перевод выполняется Gladia Translation (встроенный в WebSocket, модель `enhanced`), а не LLM.

- Проверить `GLADIA_API_KEY` в `.env` — ключ действителен?
- Gladia Translation поддерживает не все языковые пары — проверить документацию: [docs.gladia.io](https://docs.gladia.io)
- Убедиться что `target_lang: "ru"` в `config.yaml`

### LLM-подсказки не генерируются / пустой ответ

- Проверить `OPENAI_API_KEY` и `LLM_BASE_URL` в `.env`
- Проверить `OPENAI_MODEL=glm-4.7-flash` — для Z.AI обязателен `thinking:disabled` (включён автоматически через `thinkingTransport`)
- Проверить `LLM_MAX_TOKENS` ≥ 256 (рекомендуется 1024)

## Тесты

```bash
# Полный прогон (CGO обязателен для capture-тестов)
export PATH="/c/msys64/ucrt64/bin:$PATH"
go test ./... -count=1        # все тесты
go test -race ./...           # детектор гонок
go vet ./...                  # статический анализ
```

**Текущий статус:** `go vet` чист, `go test -race ./...` → 8/8 PASS.

## Ручное тестирование

```bash
# 1. Проверка аудиоустройств (требует CGO)
go run ./test/cable_test

# 2. Генерация тестового WAV с речью (Edge TTS / fallback tone)
python generate_test_wav.py

# 3. Интеграционный тест Gladia — STT + перевод + подсказки на реальных API
#    Требует test_speech.wav (генерируется шагом 2)
go run ./test/gladia_test

# 4. Тест LLM-подсказок — GenerateAnswers + GenerateAnswersStream
go run ./test/llm_test
```

### Что проверяет каждый тест

| Тест | Файл | Компоненты |
|------|------|-----------|
| `cable_test` | `test/cable_test/main.go` | VB-Cable: список устройств, проверка инициализации |
| `gladia_test` | `test/gladia_test/main.go` | Gladia: STT (Solaria-1), перевод (enhanced, EN→RU), LLM-подсказки |
| `llm_test` | `test/llm_test/main.go` | LLM: batch-генерация + SSE-стриминг подсказок |
| `generate_test_wav.py` | `generate_test_wav.py` | Генератор тестового WAV: Edge TTS → 16kHz mono PCM |

## Структура проекта

```
test/
  cable_test/       проверка VB-Cable
  llm_test/         интеграционный тест LLM-подсказок (GLM-4.7-Flash)
  gladia_test/      интеграционный тест STT + перевода (Gladia Solaria-1)
generate_test_wav.py  генератор тестового WAV-файла (Edge TTS)
internal/
  common/           Config, STTEvent
  capture/          malgo: loopback + микрофон, ресамплер
  stt/              GladiaProvider (Solaria-1, WebSocket)
  translator/       OpenAIProvider (GLM-4.7-Flash), TranslationEngine, промпты
  ui/               GioUI трёхзонный оверлей (Interim / Перевод / Подсказки)
  logger/           FileSessionLogger: CSV + MP3/WAV аудио
  pipeline/         Оркестрация: захват → STT → dispatch → UI
```

## Документация

- `ARCHITECTURE.md` — полная архитектура v4, потоки данных, схема горутин, задержка, интеграционное тестирование
- `AGENTS.md` — стандарты кода (Go, concurrency, тестирование)
- `.hermes/plans/` — планы реализации

## Лицензия

MIT
