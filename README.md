# Translator — AI-ассистент собеседований и встреч

Оверлей реального времени для Windows. Переводит речь собеседника (EN→RU),
сохраняет IT-термины, генерирует подсказки к ответам при детекции вопроса.

## Возможности

- **Двухканальный захват аудио** — loopback (собеседник) + микрофон
- **Распознавание речи** — Deepgram WebSocket (nova-2), задел под локальный Sherpa-onnx
- **Перевод** — GPT-4o-mini с сохранением IT-терминов (Deadlock, Kubernetes, CQRS…)
- **Подсказки** — 2–3 тезиса ответа при детекции вопроса
- **Прозрачный оверлей** — GioUI, всегда поверх окон, не перехватывает фокус
- **Логирование** — JSON-транскрипты + PCM-дампы аудио

## Требования

- **Go 1.22+**
- **GCC (MinGW-w64)** — для CGO (malgo + GioUI)
- **VB-Cable** ([vb-audio.com/Cable](https://vb-audio.com/Cable/)) — виртуальный аудиокабель
- **API-ключи:** Deepgram (STT) + OpenAI (перевод)

## Быстрый старт

```bash
git clone https://github.com/MasterErik/translator.git
cd translator
```

Создать `.env` с ключами (см. раздел Конфигурация).

## Сборка

```bash
# Полная сборка — запускаемый .exe
go build -o translator.exe ./cmd/app

# Без CGO (заглушка захвата — для тестов, без реального аудио)
go build -tags=!cgo -o translator.exe ./cmd/app
```

Результат: `translator.exe` в корне проекта.

## Запуск

```bash
# Основной запуск
go run ./cmd/app

# Или собранный .exe
./translator.exe
```

Остановка: `Ctrl+C` — корректное завершение, логи сохраняются в `logs/`.

## Конфигурация

### API-ключи (`.env`)

```env
DEEPGRAM_API_KEY=ваш_deepgram_ключ
OPENAI_API_KEY=ваш_openai_ключ
```

### Настройки аудио (`config.yaml`)

```yaml
# Имена устройств — указать свои или оставить пустыми (системные по умолчанию)
loopback_device: "CABLE Output (VB-Audio Virtual Cable)"
mic_device:     "Microphone (Realtek High Definition Audio)"
```

Или через переменные окружения:

```bash
set LOOPBACK_DEVICE=CABLE Output (VB-Audio Virtual Cable)
set MIC_DEVICE=Microphone (Realtek High Definition Audio)
translator.exe
```

### Как узнать имена устройств

PowerShell-модуль `AudioDeviceCmdlets` — самый удобный способ:

```powershell
# Установить модуль (один раз)
Install-Module -Name AudioDeviceCmdlets -Scope CurrentUser

# Полный список всех аудиоустройств
Get-AudioDevice -List

# Только устройства воспроизведения
Get-AudioDevice -Playback

# Только устройства записи (микрофоны)
Get-AudioDevice -Recording
```

Имена из вывода `Get-AudioDevice -List` (колонка `Name`) вставлять в `config.yaml` как `loopback_device` и `mic_device`.

Без модуля — в настройках Windows: Параметры → Звук → Управление звуковыми устройствами.

## Настройка аудиоканалов

Приложение захватывает два аудиопотока одновременно:

| Канал | Источник | Назначение |
|-------|----------|------------|
| **Loopback** | Виртуальный аудиокабель (VB-Cable) | Речь собеседника → STT → перевод на экран |
| **Микрофон** | Системный микрофон | Ваш голос → логирование (в будущем — верификация ответа) |

### Как это работает

1. **Установите VB-Cable** — создаёт виртуальное аудиоустройство `CABLE Input` / `CABLE Output`
2. **Направьте звук собеседника** в `CABLE Input`:
   - В настройках приложения для звонков (Zoom, Teams, Meet) выберите `CABLE Input` как устройство вывода
   - Или в настройках Windows: Параметры → Звук → громкость приложений → перенаправьте браузер/мессенджер
3. **Укажите имя устройства** в `config.yaml`:
   - `loopback_device` — как правило `CABLE Output (VB-Audio Virtual Cable)`
   - `mic_device` — ваш физический микрофон
4. Приложение читает звук из `CABLE Output` (loopback) и микрофона, преобразует 48kHz Stereo → 16kHz Mono, отправляет в Deepgram

Если `loopback_device` и `mic_device` пусты — используются системные устройства по умолчанию.

## Тесты

```bash
go test ./... -count=1        # все тесты
go test -race ./...           # детектор гонок (нужен GCC)
```

## Ручное тестирование STT и перевода

```bash
python generate_test_wav.py         # сгенерировать тестовую речь (Windows TTS)
go run ./cmd/manual_test            # проверить STT + перевод на реальных API
```

## Структура проекта

```
cmd/app/           точка входа + graceful shutdown
cmd/manual_test/   ручной тест STT + перевода
internal/
  common/          Config, STTEvent, UIEvent
  capture/         malgo: loopback + микрофон, ресамплер 48→16kHz Stereo→Mono
  stt/             DeepgramProvider, SherpaOnnxProvider (заглушка)
  translator/      OpenAIProvider, TranslationEngine, промпты
  ui/              GioUI-оверлей, Win32 WS_EX_TOPMOST/LAYERED
  logger/          FileSessionLogger: JSON + PCM-дамп
```

## Лицензия

MIT
