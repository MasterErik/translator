# План исправлений: разговорный контекст, retrieval (BM25), модель LLM

> Дата: 2026-11-02. Результат ревизии QA-конвейера (dispatcher → retrieval → LLM).
> Синхронизирован с `docs/qa-architecture.md`, `docs/candidate-context-architecture.md`.

## 1. Цели и критерии приёмки

| Метрика | Сейчас | Цель |
|---|---|---|
| Recall@1 (v2-holdout) | 0.429 | ≥ 0.70 |
| MRR (v2-holdout) | 0.429 | ≥ 0.60 |
| Реплики интервьюера в контексте LLM | только Q/A-пары модели | все финальные реплики (ring buffer) |
| Потерянные turn'ы истории (гонка OnTail) | есть | 0 |
| Ответ уходит из retrieval-запроса | только question | question + clarifications + последние реплики |
| TTFT LLM (reasoning включён) | ~2.3–4.3 s | ≤ 1 s (reasoning_effort=low) |
| Парсинг ответа LLM | строковый `EN: … \| RU: …` | structured JSON, fallback на старый |

Регрессионный gate — как сейчас: `context_eval` по train/holdout; новые пороги
калибруются ТОЛЬКО по train, holdout — финальная проверка.

## 2. Полная карта проблем (прожарка ревизии)

Каждая проблема-H из анализа имеет корень, постоянный фикс и фазу плана.

### Блок A — разговорный контекст («не поддерживает контекст беседы»)

| ID | Проблема | Корень | Фаза фикса |
|---|---|---|---|
| A1 | В контекст LLM попадают только Q/A-пары, причём «A» — текст модели, а не реальная речь кандидата | `ConversationHistory` хранит только `RecordAnswer`; реплики интервьюера не-вопросы нигде не копятся (кроме односессионного `lastFinal`) | F1 (SpeechLog) |
| A2 | Кандидат не транскрибируется: mic-канал идёт только в MP3-лог | `audioStream` = только loopback; mic записывается при `SAVE_AUDIO` в logger | F1b (опция, dual-channel) |
| A3 | Гоночная потеря turn'а: `RecordAnswer` только при `OnTail` | ответ на старый вопрос не пишется в историю, молча | F2 |
| A4 | Уточнение теряется: `lastFinal` — одна последняя реплика + Shift+F2 дропается при `busy` | потребляемый ресурс на 1 значение + busy-guard | F3 (auto-clarify + буфер) |
| A5 | `CommandMoreContext` — плацебо: истории «больше» нет | контекст уже обрезан до 6 turns / 4000 токенов | F1 (SpeechLog) |
| A6 | Дедуп вопросов точным совпадением текста | `q == d.lastQuestion`; STT-вариативность обходит дедуп, легитимные повторы давятся | F4 |
| A7 | Мультичастные вопросы: две генерации на один вопрос, первая устаревает | авто-генерация на каждый EndOfTurn с «?» | F3+ (агрегация, коалесинг) |

### Блок B — «неверно использует мои скилы» (retrieval)

| ID | Проблема | Корень | Фаза фикса |
|---|---|---|---|
| B1 | R@1 = 0.43 на holdout; lexical matching без стемминга/лексикона | собственный scoring, «running ≠ run», синонимия только через aliases | F5 (BM25F + стемминг) |
| B2 | Retrieval-запрос = только question; уточнения не влияют на отбор фактов | `candidateContextFn(question)` в `dispatcher.go` | F5 (query expansion) |
| B3 | technical_matrix обрезается до 100 токенов, профиль до 150 | `max_matrix_tokens: 100`, `max_profile_tokens: 150` | F0 (конфиг) + F6 (матрица как факты) |
| B4 | Стек-множители срабатывают на бытовых словах (`go`, `spring`) | известный риск v1 | F5 (BM25-нормировка снижает вес множителей) |
| B5 | Hints «use as is» с порогом 50 (калибровка 36.5–62) может перекрыть факты | `hints.min_score: 50`, `top_k: 1` | F0 (порог/приоритет) |
| B6 | System-промпт воспроизводит ошибку retrieval как уверенный отказ/чужой факт | «use only provided facts» без graceful-формулировки | F8 (промпт) |
| B7 | Оценка `EstimateTokens=(len+3)/4` неверна для RU | нет tokenizer'а | F9 (низкий приоритет) |

### Блок C — конвейер и надёжность

| ID | Проблема | Корень | Фаза фикса |
|---|---|---|---|
| C1 | Задания из очереди дропаются молча, UI-метка зависает «генерация…» | переполнение `answerCh` → SetState без пуша шапки | F10 |
| C2 | `context.Background()` в `generateAnswers` — shutdown генерирует LLM-ответы | таймаут не привязан к `workerCtx` | F10 |
| C3 | 429-ретраи без учёта `Retry-After`; AnswerTimeout фиксированный | `createChatCompletionWithRetry` | F7 |
| C4 | Дизайн-док и конфиг расходятся (ARCHITECTURE.md: GLM-4.7-Flash; config: `openai/gpt-oss-20b`) | дрейф доков | F-docs |
| C5 | Ответ LLM парсится строковым фильтром «EN: … \\| RU: …», до 3 строк | reasoning-модели ломают формат | F7 (JSON output) |
| C6 | Non-стимингый показ ответа (синхронная генерация в zone 4) | `GenerateAnswersStream` есть, но не используется в диспетчере | F7 (стриминг) |

## 3. Фазы

### F0. Конфигурационные быстрые победы (≈0.5 дня, без кода логики)
1. `candidate_context.max_matrix_tokens: 100 → 512`, `max_profile_tokens: 150 → 400`.
   Задержка латентности несущественна (gpt-oss-20b на 131k ctx).
2. `hints.min_score: 50 → 58` (порог отвечает только если у hints-факта score
   фактического вопроса заметно выше посторонних; калибровка — `context_eval`).
3. `hints.top_k: 1 → 2` и правило приоритета в промпте: hints-подсказка —
   «candidate-provided suggestion», не отмена фактов.
4. `conversation.recent_turns: 6 → 10` (с учётом SpeechLog из F1).
5. Фикс доков: ARCHITECTURE.md → фактическая модель из config.yaml (C4).

### F1. SpeechLog — полный разговорный контекст (блок A1, A5) [ядро плана]
Новый компонент в dispatcher (или `internal/speechlog`):
- Все финальные реплики интервьюера (EndOfTurn, вопрос ИЛИ не-вопрос) пишутся в
  ring buffer `[]SpeechRemark{Text, Timestamp, Role}` (вместо единственного
  `lastFinal` — это же решает A4-буфер).
- Реплики-НЕвопросы, пришедшие ПОСЛЕ последнего вопроса метки idx и ДО её
  ответа, автоматически прикрепляются к метке как clarifications
  (**auto-clarify**), Shift+F2 остаётся ручным оверраем (добавить/убрать).
- `BuildContext` расширяется: `ConversationContext` = Q/A-пары + блок
  `Interviewer remarks:\n- …` из SpeechLog (последние K реплик, бюджет токенов).
### F1b. Кандидат-канал (dual-channel, A2) — отменено
> Решение (2026-11-02): транскрипция вашей речи в контекст НЕ включается.
> ConversationContext — только реплики интервьюера и Q/A-пары; A2 закрывается
> как «won't fix». MP3-запись mic при `SAVE_AUDIO` остаётся как есть.
Принять: интервал антидребезга (реплики <1s склеивать), роль помечается.

### F2. Фикс `RecordAnswer` (A3)
- `RecordAnswer(question, answer)` выполняется **всегда** после успешной
  генерации задания, независимо от `OnTail` — дедуп по тексту вопроса уже есть
  (`ConversationTurn` matching в `RecordAnswer`).
- Для команды-модификаторов СБЕРЕЧЬ СЕМАНТИКУ: ответ пишется только для
  CommandAnswer либо всегда, если для этой метки ещё не было финального
  `CommandAnswer`-ответа.
- Интеграция в `ConversationHistory` остаётся; хронология = время вопросов.

### F4. Дедуп и мультичастность (A6, A7)
- Дедуп: нормализация (lowercase, пунктуация, стоп-фразы "so", "ok", "right",
  "yeah"), fuzzy-сравнение (token-Jaccard ≥ 0.75) против последних 3 меток.
- Мультичасть: если новая метка добавляется в течение N=6s после предыдущей
  И предыдущая ещё QQueued/QGenerating — не новая генерация, а склейка: текст
  дописывается в вопрос той метки, её задание перегенерируется (коалесинг).
- Авто-ответ по умолчанию остаётся, но late-фрагменты НЕ порождают вторую
  метку (идут как уточнение).

### F5. Retrieval: BM25F + стемминг + query expansion (B1, B2, B4) [ядро плана]

> **Решение (2026-11-02):** своя реализация BM25F на текущем индексном
> движке пакета `internal/context` (tf/avgdl добавляются в index.json);
> зависимость — только `github.com/blevesearch/snowballstem` (english,
> чистый Go, без CGO). Отдельная библиотека bleve не подключается.

Изменения в `internal/context` (пакет сохраняет API `BuildContextFn`):

1. **BM25F вместо самодельного field-weighted score.** Текущая формула
   `field(5/4/3/2) × IDF + phrase + section` → перейти на BM25F по полям:
   keywords(w=5), title(w=4), aliases(w=3), content(w=2), section-score —
   как отдельный additive-канал. Требуемые данные в индексе уже есть или
   легко расширяемы: `tf` по факту/полю и `avgdl` по полю; dfMap есть.
   Формула: `score = Σ_f w_f · IDF(t) · (tf·(k1+1)) / (tf + k1·(1−b+b·dl/avgdl))`,
   k1=1.2, b=0.75 (tunable как сейчас scores.go).
2. **Стемминг**: `github.com/blevesearch/snowballstem` (english, чистый Go,
   без CGO) в `tokenize.go` step Canonicalize; `TokenizerVersion++` —
   существующий механизм freshness пересоберёт index.json автоматически.
3. **Phrase-бонус** остаётся (title +3/content +1, cap +6) — поверх BM25F.
4. **Стек-множители** перепроецируются на BM25-масштаб: Penalty/Bonus тем же
   кодом, но relative-threshold считать по Core (как сейчас M7a) — порог
   0.6 перкалибровать по train (может сместиться ~0.7).
5. **Query expansion (B2):** retrieval-запрос = question + clarifications +
   последние 2 реплики интервьюера из SpeechLog. `BuildContextFn(question, session)`
   → `BuildContextFn(query Request)` — меняется сигнатура (одна точка
   интеграции в `dispatcher.generateAnswers`).
6. **min_score** пересчитать в BM25-единицах: порог как доля от top1.Score
   (relative), абсолютный порог убрать или калибровать по eval-квантилю.
7. **Синонимы**: `global_aliases` в manifest.json используются в обе стороны
   (словарь синонимов для query expansion), формат не меняется.

Gate: `context_eval` по v2-train; порог принятия — R@1/MRR не ниже текущих,
LeakRate не выше 0.116. Только после — holdout.

### F6. Индексация документов (по идеям AnswerCue/NexQ)
- Новый источный каталог `docs_base/` рядом с `candidate_context/`:
  MD/TXT — загружаются напрямую; **PDF/DOCX → Markdown** с фактовыми
  маркерами (разметка делает LLM-шаг offline — тот же CLI/build flow, без
  runtime-зависимостей: пакеты рассматреть `github.com/ledongthuc/pdf` или
  go-fitz для PDF, `unioffice`/docx→md — отдельным инструментом в `test/`).
- Все документы конвертируются prepend'ом в секции `sections/*.md` того же
  формата (маркеры `<!-- fact {...} -->`) — retrieval/BM25/eval работают
  без изменений. Индекс пересобирается по существу freshness-чеком.
- **Матрица навыков (B3):** строки technical_matrix переводятся в факты с
  типом `matrix` (теги=стек), тогда матрица перестаёт быть хвостом,
  режущимся бюджетом — в prompt идут релевантные строки под запрос.
- Опции: `source_dirs: [candidate_context, docs_base]` в config.yaml
  (список); max_tokens фактов общий.
- Отложено: file watcher (fsnotify) — по умолчанию off, индекс при старте.

### F7. LLM: модель + формат + стриминг (C3, C5, C6) [и смена модели]
- **JSON output** (`response_format: {type: json_object}` для
  non-reasoning-моделей; для gpt-oss — та же инструкция в system): схема
  `{"answers":[{"source":"…","target":"…"}]}` (1–3 варианта), fallback —
  текущий строковый парсер. Убирает `parseAnswerHints`-эвристику из
  критического пути (оставить как fallback).
- **Стриминг в UI**: диспетчер переключается на `GenerateAnswersStream`
  (уже реализован), метка QGenerating и в zone 4 показывает частичный
  ответ; SetState(QReady) по завершении — текст метки не отличается для
  ConversationHistory.
- **Ретраи**: читать `Retry-After` из 429 (go-openai supports
  APIError), jitter; `AnswerTimeout` в config (`answer_timeout: 12s`).
- **Кандидаты модели** (см. §4): оба параметр `reasoning_effort` для
  gpt-oss; fallback-цепочка в конфиге `llm.fallback_models: [qwen, glm]`.

### F8. System-промпт (B6)
- Смягчить graceful: «If the provided facts do not cover the question,
  answer with clearly marked generic knowledge and do NOT attribute it to
  the candidate's experience».
- Явный пример correct/incorrect пары границ стеков (не attribution).
- Разделение режима JSON и строкового формата в шаблоне (F7).

### F9. Tokenizer (B7, отложено)
- Замена EstimateTokens на tokenizer go-tiktoken-cl100k (EN) — RU-тексты
  значительно точнее. Низкий приоритет: gpt-oss 131k ctx, свобода.

### F10. Надёжность (C1, C2)
- `generateAnswers`: `context.WithTimeout(workerCtx, AnswerTimeout)` —
  shutdown мгновенно отменяет генерации (drain прыжостой).
- Дроп задания: пуш шапки метки в QIdle с текстом «MISSED — press F1»
  (state Line для zone 3 уже есть).
- Метрика: счётчик дропов в sessLog раз/10s (уже есть ReportDrop).

### F11 (опция). Гибрид с локальными embeddings
- `github.com/anush008/fastembed-go` (bge-small-en-v1.5, ONNX, локально,
  ~130MB веса, без GPU).
- RRF-фьюжн (k=60) BM25F-топ-K и dense-топ-K. Флаг `candidate_context.hybrid: true`.
- Включать только если после F5 R@1 < 0.70 (микроцикл: eval решает).

## 4. Смена модели — сравнение (Groq, 2026-11)

| Модель | t/s | TTFT/европ. полный ответ | Цена in/out /1M | Русский | Вердикт |
|---|---|---|---|---|---|
| `openai/gpt-oss-20b` (+`reasoning_effort="low"`) | 1000 | ~0.7 s / ~1.5 s | $0.075 / $0.30 | средне | **Основная**: min латентность, дешёвая; формат JSON отличная follow,Simple English правила легко |
| `openai/gpt-oss-120b` | 500 | ~0.7 s / ~6 s | $0.15 / $0.60 | хороший | Режим «MoreContext/F-модификатор» — дольше думать, выше качество |
| `qwen/qwen3.6-27b` | 450 | ~0.9 s / ~2–3 s | $0.60 / $3.00 | отличный | Fallback: лучший русский из доступных на Groq |
| Z.AI `GLM-4.7-Flash` (вне Groq, конфиг уже это упоминал) | — | ~0.4 s / ~1–2 s | cheap/free tier | отличный | Альтернативная база OpenAI-совместимая, уже описана в ARCHITECTURE.md |

Запланировать (сейчас всё в ARCHITECTURE.md путано): 
`LLM_MODEL=openai/gpt-oss-20b` + `LLM_REASONING_EFFORT=low` + стриминг (F7),
fallback-цепочка `["qwen/qwen3.6-27b", "glm-4.7-flash"]`.

> **Решение (2026-11-02):** основная модель — `openai/gpt-oss-20b` с
> `reasoning_effort=low` и стримингом; А1 — подтверждено. Техническая матрица:
> бюджет 512 (F0) + перевод строк матрицы в факты (F6).
Критерий выбора — итог A/B на llm_bench (уже есть `llm_bench_test.go`) +
2–3 реальных вопроса RU-перевода в каждой модели; качество фактов решает
retrieval (F5), не модель.

**Важно про reasoning-модели:** gpt-oss/qwen при reasoning кладут
chain-of-thought в content (см. существующий `stripThinking` в openai.go);
`reasoning_effort: "low"` + JSON-инструкция резко снижают мусорные токены.

## 5. Идеи из аналогичных проектов (что берём / не берём)

| Проект | Идея | Решение |
|---|---|---|
| interview-copilot (Electron) | dual-channel транскрипция: mic = кандидат | ❌ отклонено (решение 2026-11-02): только реплики интервьюера |
| interview-copilot | JD-контекст, «target role» | в факты как факт `jd` (job description) + `interview.target_stack` уже есть |
| AnswerCue | prep-chat + reusable docs + полная хронология интервью | F6 (docs) + F1 (SpeechLog вместо only-Q/A) |
| NexQ | локальный RAG, 100% local | направление F11 deferred; формат документов — F6 |
| OpenCluely | сегментация на паузы / отсев призраков STT | учтено в F4 (склейка фрагментов, anti-noise: склейка помеченных фраз так и так приходят turn-aware от Gladia) |
| Second Brain | resume+JD grounding, многоязык | уже есть (candidate context + target_stack + lang pair) |
| Все | disclaimer о теле/ethics | в README уже есть? проверять (вне плана) |

Не берём: скриншот-анализ (DeepSeek/Gemini vision), мобильный компаньон,
recap-модуль — вне приоритетов.

## 6. Порядок работ и риск

```
Ф0 (конфиг) → F2 (RecordAnswer) → F1 (SpeechLog) → F5 (BM25F) → F4 → F7 → F6 → F10
```
- F1/F4 затрагивают dispatcher — перед мержем гонка в reruns
  `questions_concurrent_test.go`, `duplicate_question_test.go`.
- F5 — highest возвращаемая значение, но и максимальный инженерный риск:
  делается за отдельный PR с полной перепроверкой eval-датасетов.
- Каждый этап: `go fmt`, `go vet`, `go test ./...`, Conventional Commits
  (per репо-правил AGENTS.md); e2e (`-tags e2e`) — только планировщик.
- Estimate: F0+F2+F1 ≈ 1.5–2 дня; F5 ≈ 1.5 дня; F7 ≈ 1 день; F4/F6 ≈ 1 день;
  F10 ≈ полдня.
