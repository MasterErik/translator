# Translator — AI-ассистент собеседований и встреч

Оверлей реального времени для Windows: переводит речь собеседника (EN→RU), сохраняет IT-термины, генерирует подсказки к ответам.

## Возможности

- **Двухканальный захват аудио** — loopback (собеседник) + микрофон (ваш голос)
- **Распознавание речи** — Deepgram WebSocket (nova-2), задел под локальный Sherpa-onnx
- **Перевод** — GPT-4o-mini с сохранением IT-терминов (Deadlock, Kubernetes, CQRS…)
- **Генерация подсказок** — 2–3 тезиса ответа при детекции вопроса
- **Прозрачный оверлей** — GioUI, всегда поверх окон, не перехватывает фокус
- **Логирование сессий** — JSON-транскрипты + PCM-дампы аудио

## Требования

- **Go 1.22+**
- **GCC (MinGW-w64)** — для CGO (malgo)
- **VB-Cable** или аналог — виртуальный аудиокабель для loopback-захвата
- **API-ключи:** Deepgram (STT) + OpenAI (перевод)

## Установка

```bash
git clone <repo-url> translator
cd translator
```

## Конфигурация

Создать `.env` файл в корне проекта:

```env
DEEPGRAM_API_KEY=ваш_deepgram_ключ
OPENAI_API_KEY=ваш_openai_ключ
```

Несекретные параметры — в `config.yaml` (модель, язык, пути логов, CV-контекст).

## Сборка

```bash
# Полная сборка (с CGO — реальный захват аудио)
go build -o translator.exe ./cmd/app

# Без CGO (заглушка захвата — для тестов)
go build -tags=!cgo -o translator.exe ./cmd/app
```

## Запуск

```bash
# Основной запуск
go run ./cmd/app

# Запуск без CGO (тихие PCM-фреймы, без реального аудио)
go run -tags=!cgo ./cmd/app
```

Остановка: `Ctrl+C` — корректное завершение с сохранением логов.

## Ручное тестирование

```bash
# Сгенерировать тестовую речь (Windows TTS)
python generate_test_wav.py

# Проверить STT + перевод на реальных API
go run ./cmd/manual_test
```

## Тесты

```bash
go test ./... -count=1        # все тесты
go test -race ./...           # детектор гонок (нужен GCC)
```

## Структура проекта

```
cmd/app/           точка входа + graceful shutdown
cmd/manual_test/   ручной тест STT + перевода
internal/
  common/          Config, STTEvent, UIEvent
  capture/         malgo: loopback + микрофон, ресамплер
  stt/             DeepgramProvider, SherpaOnnxProvider (заглушка)
  translator/      OpenAIProvider, TranslationEngine, промпты
  ui/              GioUI-оверлей, Win32 WS_EX_TOPMOST/LAYERED
  logger/          FileSessionLogger: JSON + PCM-дамп
```

## Лицензия

MIT
