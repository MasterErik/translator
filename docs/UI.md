### UI — четыре зоны (GioUI v0.10.1)

```
┌──────────────────────────┐
│ I have five years of...  │ ← Зона 1: Interim (речь, 2 строки, белый текст)
├──────────────────────────┤ separator 3px
│ EN: We use Redis for...  │ ← Зона 2: TranslationHistory (скролл, макс 10 строк)
│ RU: Мы используем Redis  │           оригинал + перевод от Gladia
├──────────────────────────┤ separator 3px
│ We use Redis for caching │ ← Зона 3: TranscriptionHistory (скролл, макс 5 строк)
│ and message brokering    │           оригинал речи на английском
├──────────────────────────┤ separator 3px
│ EN: Redis is...          │ ← Зона 4: AnswerCandidates (1 подсказка, только вопросы)
│ RU: Redis — это...       │           формат: EN: ... | RU: ...
└──────────────────────────┘
```

**Параметры окна:**
- Размер: 800×650 (из `.env`: `OVERLAY_WIDTH`, `OVERLAY_HEIGHT`)
- Заголовок: пустой (screen sharing privacy)
- Заголовки зон: **отсутствуют** — только разделители 3px между зонами
- Позиционирование: `app.TopMost(true)` — поверх других окон
- Стили: **без** `WS_EX_LAYERED`/`WS_EX_TRANSPARENT` (ломают рендеринг Gio)
- Win32: `WS_EX_NOACTIVATE` через `findWindowByPID`
- Принцип: минимальные решения, без лишних HWND-стилей — `TopMost(true)` достаточно

**Автоскролл:**
- `prevHistLen` — сохраняется предыдущая длина списка
- `layout.List` — персистентный, не пересоздаётся
- При добавлении элемента: `ScrollTo(n-1)` — прокрутка к последнему

**Запуск:** `app.Main()` **не используется** — вместо него кастомный event loop.

**Тест:** `TestWindowStarts` — 40 строк, проверка первого и последнего элемента списка.
