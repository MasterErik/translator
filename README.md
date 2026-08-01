# Translator — AI-ассистент собеседований и встреч

Оверлей реального времени для Windows. Распознаёт речь собеседника (EN), переводит EN→RU (Gladia), генерирует подсказки при детекции вопроса (LLM).

## Требования

- **Go 1.22+**
- **GCC (MinGW-w64)** — MSYS2 `C:\msys64\ucrt64\bin`
- **VB-Cable** ([vb-audio.com/Cable](https://vb-audio.com/Cable/))
- **API-ключи:** [Gladia](https://gladia.io) (STT + перевод) + [Z.AI](https://z.ai) (LLM-подсказки)

## Быстрый старт

```bash
git clone https://github.com/MasterErik/translator.git
cd translator
cp .env.example .env   # заполнить GLADIA_API_KEY и LLM_API_KEY
```

## Сборка и запуск

```bash
# Быстрый запуск (компиляция + запуск одной командой, без консоли)
export PATH="/c/msys64/ucrt64/bin:$PATH"
go run -ldflags="-H windowsgui" .

# Production (нужен GCC, без консольного окна)
export PATH="/c/msys64/ucrt64/bin:$PATH"
go build -ldflags="-H windowsgui" -o translator.exe .

# Разработка (без GCC — заглушка аудио, без консоли)
CGO_ENABLED=0 go build -ldflags="-H windowsgui" -o translator.exe .

# Запуск собранного бинарника (без консоли)
./translator.exe
```

> **Примечание:** `-ldflags="-H windowsgui"` скрывает консольное окно. Без него Windows открывает пустой терминал при запуске.

Остановка: `Ctrl+C`.

## Конфигурация

Приоритет: **`.env` > `config.yaml`**. Минимальный `.env`:

```env
GLADIA_API_KEY=your_key
LLM_API_KEY=your_key
LLM_BASE_URL=https://api.z.ai/api/paas/v4/
```

Аудиоустройства (опционально):

```env
LOOPBACK_DEVICE=CABLE Input (VB-Audio Virtual Cable)
MIC_DEVICE=Microphone (Realtek High Definition Audio)
```

Узнать имена устройств: `go run ./test/cable_test`

Полный шаблон: `.env.example`

## Настройка аудио

```
Chrome/Teams → CABLE Input (Playback) → WASAPI Loopback → Translator
Микрофон     → WASAPI Capture          → Translator
```

| Канал | Устройство | Назначение |
|---|---|---|
| Loopback | `CABLE Input` (VB-Cable, Playback) | Речь собеседника → STT → перевод |
| Микрофон | Системный микрофон (Recording) | Ваш голос → логирование |

## Документация

| Файл | Для чего |
|---|---|
| `docs/ARCHITECTURE.md` | Архитектура, диаграммы, интерфейсы, shutdown |
| `docs/TESTING.md` | Тесты, SLA, performance constraints |
| `docs/UI.md` | Четырёхзонный overlay |
| `.env.example` | Все переменные окружения |

## Лицензия

MIT
