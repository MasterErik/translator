# Translator — AI-ассистент собеседований и встреч

Оверлей реального времени для Windows. Переводит речь собеседника (EN→RU),
сохраняет IT-термины, генерирует подсказки к ответам при детекции вопроса.

## Возможности

- **Двухканальный захват аудио** — VB-Cable (собеседник) + микрофон
- **Распознавание речи** — Deepgram WebSocket `/v2/listen` (Flux), задел под локальный Sherpa-onnx
- **Стриминг-перевод** — GLM-4.7-Flash (Z.AI), токены в UI инкрементально, задержка ~1s
- **Трёхзонный UI** — текущая речь / история перевода (10 строк) / подсказки
- **Цветовой индикатор** — янтарный (ожидание) → зелёный (стриминг) → тёмный (готово)
- **Подсказки** — 2–3 тезиса ответа при детекции вопроса
- **Прозрачный оверлей** — GioUI, всегда поверх окон, не перехватывает фокус
- **Логирование** — CSV-транскрипты + MP3/WAV дампы аудио

## Требования

- **Go 1.22+**
- **GCC (MinGW-w64)** — обязателен для production-сборки, путь: MSYS2 `C:\msys64\ucrt64\bin`
- **VB-Cable** ([vb-audio.com/Cable](https://vb-audio.com/Cable/)) — виртуальный аудиокабель
- **API-ключи:** Deepgram (STT) + Z.AI (перевод, OpenAI-совместимый)

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
DEEPGRAM_API_KEY=ваш_deepgram_ключ
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
deepgram_model: "flux-general-en"
openai_model: "glm-4.7-flash"
llm_base_url: "https://api.z.ai/api/paas/v4/"
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

### Пустой перевод / английский текст вместо перевода

- Проверить `LLM_BASE_URL` и `OPENAI_API_KEY` в `.env`
- Проверить `LLM_MAX_TOKENS` ≥ 1024 (reasoning-токены!)
- `thinking:disabled` включён автоматически для `api.z.ai`

## Тесты

```bash
export PATH="/c/msys64/ucrt64/bin:$PATH"
go test ./... -count=1        # все тесты
go test -race ./...           # детектор гонок
go vet ./...                  # статический анализ
```

## Ручное тестирование

```bash
go run ./test/llm_test          # тест перевода (GLM-4.7-Flash, стриминг, подсказки)
go run ./test/cable_test        # проверка VB-Cable
go run ./test/deepgram_test     # STT + перевод на реальных API
```

## Структура проекта

```
test/
  cable_test/       проверка VB-Cable
  llm_test/         интеграционный тест LLM (перевод + стриминг + подсказки)
  deepgram_test/    ручной тест STT + перевода
internal/
  common/           Config, STTEvent
  capture/          malgo: loopback + микрофон, ресамплер
  stt/              DeepgramProvider (Flux v2), SherpaOnnxProvider (заглушка)
  translator/       OpenAIProvider (GLM-4.7-Flash), TranslationEngine, промпты
  ui/               GioUI трёхзонный оверлей (Interim / Перевод / Подсказки)
  logger/           FileSessionLogger: CSV + MP3/WAV аудио
```

## Документация

- `ARCHITECTURE.md` — полная архитектура v3, потоки данных, схема горутин
- `AGENTS.md` — стандарты кода (Go, concurrency, тестирование)
- `.hermes/plans/` — планы реализации

## Лицензия

MIT
