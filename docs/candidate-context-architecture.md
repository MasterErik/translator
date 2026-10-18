# Candidate Context — fact-level retrieval (архитектура блока)

> Пакет `internal/context` (package name `candidatecontext`).
> Термин: **fact-level lexical retrieval with section-aware scoring**.

Формат данных (manifest/index/markers/aliases) — в `docs/candidate-context-format.md`.
Здесь — архитектура: компоненты, жизненный цикл, поток retrieval, scoring, интеграция.

## Зачем

Полная база кандидата никогда не передаётся в system prompt. В prompt попадает
только компактный профиль + точечно релевантные факты по текущему вопросу.
Retrieval локальный, lexical, только stdlib — без embeddings, vector DB и
внешних сервисов. Index строится offline и пересобирается автоматически при
устаревании на startup.

## Структура данных

```text
candidate_context/
  manifest.json      # profile + global_aliases + section metadata (source data)
  index.json         # runtime retrieval index (генерируется/пересобирается)
  sections/*.md      # source data: факты размечены markers <!-- fact {...} -->
```

Единица retrieval — `Fact`, не `Section`. `Section` даёт только
section-aware бонус к score, не является hard-фильтром.

## Компоненты пакета `internal/context`

| Файл | Ответственность |
|---|---|
| `types.go` | `FileMeta`, `Section`, `Fact`, `Manifest`, `Index`, `RetrievalResult`, `CandidateContext.Render()` |
| `load.go` | `LoadManifest`, `LoadIndex`, `CheckIndexFresh`, `LoadCandidateContext` |
| `tokenize.go` | `Tokenizer`: Normalize→Substitute→Tokenize→RemoveStopWords→Canonicalize; `TokenizerVersion` (входит в freshness) |
| `builder.go` | `BuildIndex` (markers→facts, offsets, SHA256), `SaveIndex` (temp+rename) |
| `score.go` | Веса полей, IDF, phrase/bigram matching |
| `retrieval.go` | `Retriever`: dfMap, title/section tokens, ranking, точечное чтение тел |
| `budget.go` | `Budgeter`: раздельные бюджеты profile/matrix/facts, UTF-8 safe обрезка |
| `contextfn.go` | `BuildContextFn` — единый вход сборки контекста (retrieval→budget→render→hints) |
| `stackdet` | `stackdet.Detect`: детерминированное определение целевого стека из вопроса |
| `eval.go` | `EvalQuery`, `EvalDataset`, `EvalMetrics`, `Evaluate` (сырой ranking) и `EvaluateContextFn` (итоговый prompt: Recall@k/MRR/LeakRate/EmptyRate/FalseShowRate) |
| `eval_validate.go` | Валидатор v2-набора: источники меток, дубли, `no_relevant` |

## Жизненный цикл (startup)

```text
LoadCandidateContext(root)
  1. LoadManifest(root/manifest.json)
  2. LoadIndex(root/index.json)
  3. CheckIndexFresh: версия index + SHA256 manifest.json + SHA256/размер source-файлов
  4. свежий → вернуть (manifest, index)
  5. устарел → BuildIndex → SaveIndex (безопасная замена) → вернуть пересобранные
```

Stale при: изменении manifest.json (включая global_aliases и profile), изменении
размера/SHA256 source-файла, несовместимой версии index, несовпадении
`tokenizer_version` (включая отсутствие — иначе индекс со старой семантикой
токенов молча остался бы «свежим»), отсутствии ожидаемого source-файла.
Source-файлы не меняются в runtime — file watcher/locking не нужны.

При ошибке загрузки/пересборки поведение задаёт `on_load_error` (D15):
`fail` (default) — startup завершается ошибкой; `degraded` — сессия без фактов
(`Degraded=true`). Legacy-путь (`CandidateContextFile`) включается только явным
`allow_legacy_fallback` (default false) — иначе ошибка сборки не подменяется
молча нерелевантным файлом.

## Поток retrieval (на каждый вопрос)

```text
вопрос
  → Tokenizer.Process(question)                       # canonical tokens, без stop words
  → section score (tags 3 × IDF + summary 2 × IDF) × sectionWeight(0.3)
  → fact score = keywords(5) + title(4) + aliases(3) + content(2)   [× IDF]
  → fact score == 0 → отброс                         # section metadata не создаёт результат
  → content-only gate                                # M7a: нет попаданий в keywords/title/aliases
                                                     #  → matched_content_IDF / question_IDF < τ (0.15) → отброс
  → + section bonus + phrase bonus                   # только при fact score > 0; phrase: title +3 / content +1, cap +6
  → стек-множитель (M7b)                             # Core = (field + phrase) × mult
  → relative threshold (M7a)                         # Core < top1.Core × 0.6 → отброс
                                                     # top1 — среди неоштрафованных, БЕЗ section-бонуса
  → score < minScore → отброс                        # сравнение по Raw (без множителя, D7)
  → сортировка Rank DESC, tie-break по FactID ASC
  → topK → точечное чтение тел через io.NewSectionReader
  → Budgeter: profile ≤ MaxProfileTokens, technical_matrix ≤ MaxMatrixTokens,
    факты целиком ≤ MaxTokens
  → CandidateContext.Render() → AnswerRequest.CandidateContext (prompt-ready)
```

Пустая выдача — штатный режим: нет кандидатов или все отсечены порогами → пустой
набор фактов, без ошибки.

Три величины score (шов под стек-множители, M7a):

| Величина | Формула | Назначение |
|---|---|---|
| `Raw` | `field + section + phrase` | прежняя семантика score: `min_score` и абсолютный порог hints сравниваются с ней |
| `Core` | `(field + phrase) × mult` | сопоставимая часть для relative threshold (без section-бонуса) |
| `Rank` | `Core + section` | сортировка выдачи и `RetrievalResult.Score` |

Диагностика: `RetrieveOptions{Explain: true}` заполняет `ScoreBreakdown`
(вклады полей, section/phrase, raw/core/rank, `content_ratio`, множитель,
вердикты gate/threshold), а `ExplainQuestion` показывает и отклонённых
кандидатов с причиной. Прод-путь платит за объяснение только при флаге.
Инструменты: `test/context_explain` (разложение по вопросу/тегу).

`IDF(term) = log((N+1)/(df+1)) + 1`; `df` считается по фактам (каждый факт
максимум один раз на термин), TF не учитывается. Raw source файлы НЕ читаются
для ranking — только после ranking, для отобранных фактов.

## Определение стека

`stackdet.Detect(questionTokens, sessionStack, families)` разводит три множества
стеков (полные правила и примеры — `docs/candidate-context-multipliers.md`):

| Множество | Смысл |
|---|---|
| `Penalty` | session ∪ однозначные упоминания в вопросе — вне него чужой стек штрафуется |
| `Named` | однозначно названные в вопросе стеки — только они получают бонус |
| `Exempt` | стеки, упомянутые неоднозначно (например `spring` у `java`) — освобождены от штрафа, без бонуса |

Коэффициенты (`stackdet.DefaultParams`): `Penalty 0.3`, `Bonus 1.5`,
`Session 1.0` (нейтрален, M7b.5). Пустая сессия и вопрос без упоминаний
стеков → `Penalty` пусто → множитель `1.0` (штрафов нет). Источник семейств —
`manifest.tech_families` (`Family{Name, Display, Members, Ambiguous}`).

## Explain-режим

`RetrieveOptions{Explain: true}` заполняет `RetrievalResult.Breakdown`
(`ScoreBreakdown`): вклады keywords/title/aliases/content, section и phrase
бонусы, raw/core/rank, `content_ratio`, множитель и reason, вердикты
content-gate/relative-threshold. `ExplainQuestion` дополнительно показывает
отклонённых кандидатов с причиной. Прод-путь платит за объяснение только при
флаге. Инструмент: `test/context_explain -dir … -dataset … -tag … -limit N` —
печатает строки вида `rank=… raw=… core=… ratio=… kw=… ti=… al=… ct=… sec=…
ph=… mult=…(reason) | pass/REJ …`.

## Render и prompt (M9)

`CandidateContext.Render()` собирает prompt-ready строку: `Candidate profile:` →
`Technical matrix:` (при непустой) → факты. Каждый факт предваряется меткой
`[Java · 2018–2024 · primary]` (стек через `Family.Display`, период
`YYYY-YYYY → YYYY–YYYY` (en dash) / `-present` / bare year, отсутствующие поля
опускаются; пустая метка не печатается). `RetrievalResult` несёт
`Stack`/`Period`/`Weight`/`StackDisplay` — display разрешается при materialize
из `TechFamilies`, поэтому `Render` не требует manifest.

- Пустая выдача (ноль фактов) → `EmptyRetrievalNotice`: «No relevant facts
  found — rely on the candidate profile and Technical Matrix; do not invent
  projects.»
- `RetrievalResult.OutOfTarget` → пометка перед телом «OUT OF TARGET STACK — use
  only if the question is directly about it» (hints-факт чужого стека не
  выдаётся вовсе — D8).
- `BuildSystemPrompt` (internal/translator) дополнен инструкцией M9.5:
  использовать только факты, относящиеся к технологии вопроса; не приписывать
  опыт одного стека другому.

## Интеграция

- `pipeline.New`: при `CandidateContext.Dir != ""` → `LoadCandidateContext` →
  `NewRetriever` + `NewBudgeter` → `candidateContextFn(question)` в dispatcher.
  При `Dir == ""` — legacy путь (`CandidateContextFile` читается целиком).
  Приоритет `Dir` над `CandidateContextFile`.
- `dispatcher.generateAnswers`: если `candidateContextFn` задан — вызывает его
  с текущим вопросом и кладёт результат (уже prompt-ready строка) в
  `AnswerRequest.CandidateContext`.
- `BuildSystemPrompt` получает уже собранный prompt-ready контекст и дополнен
  инструкцией M9.5 (использовать только факты нужной технологии; не приписывать
  опыт одного стека другому).
- Сборка контекста выполняется в одной горутине диспетчера (retrieval не
  конкурентный): `Retrieve*` не мутирует `Retriever`; состояние индекса
  read-only после загрузки.

## Конфигурация

`candidate_context` (config.yaml, lowercase) / `CANDIDATE_CONTEXT_*` (env):

| Параметр | Default | Назначение |
|---|---|---|
| `dir` / `CANDIDATE_CONTEXT_DIR` | `""` | путь к каталогу candidate_context; `""` = legacy |
| `max_tokens` / `CANDIDATE_CONTEXT_MAX_TOKENS` | `2000` | бюджет фактов |
| `max_profile_tokens` / `CANDIDATE_CONTEXT_MAX_PROFILE_TOKENS` | `150` | бюджет профиля |
| `max_matrix_tokens` / `CANDIDATE_CONTEXT_MAX_MATRIX_TOKENS` | `100` | бюджет `technical_matrix` |
| `min_score` / `CANDIDATE_CONTEXT_MIN_SCORE` | `0` | минимальный lexical score (0 = без порога) |
| `top_k` / `CANDIDATE_CONTEXT_TOP_K` | `5` | максимум фактов |
| `relative_threshold` / `CANDIDATE_CONTEXT_RELATIVE_THRESHOLD` | `0.6` | относительный порог (доля лучшего core; совпадает с `DefaultRelativeThreshold` в `score.go` и с дефолтом `applyDefaults` в `config.go`) |
| `allow_legacy_fallback` / `CANDIDATE_CONTEXT_ALLOW_LEGACY_FALLBACK` | `false` | D14: разрешить legacy-файл при ошибке сборки |
| `on_load_error` / `CANDIDATE_CONTEXT_ON_LOAD_ERROR` | `fail` | D15: `fail` \| `degraded` |

Отдельно `interview` (env `INTERVIEW_TARGET_STACK`): `target_stack` — целевой
стек интервью, список (YAML `[java, php]` или строка через запятую). Default
пусто (D2) — штрафов нет.

Нулевой бюджет здесь означает «поле не задано» → подставляется default с WARN
на старте (не «без лимита»); отрицательный — ошибка старта.

`Retriever` не знает о budget; `Budgeter` не знает о `min_score` и tokenizer.

Generic env-override (`config.go`): top-level скаляры переопределяются
переменной окружения по yaml-тегу (reflection, string/int/bool). Вложенные
структуры (`candidate_context`, `hints`, `interview`) и списки
(`interview.target_stack`) разбираются явным кодом — reflection их не трогает.
Невалидное значение `CANDIDATE_CONTEXT_ON_LOAD_ERROR` — громкая ошибка старта.

## Регулируемые параметры скоринга

Стек-множители (M7b) и пороги (M7a) — tuning-параметры. Дефолты живут в коде
(`stackdet.DefaultParams`, `DefaultContentGateTau`, `DefaultRelativeThreshold`);
для калибровки переопределяются CLI-флагами `test/context_eval` (`-penalty`,
`-bonus`, `-session`, `-gate-tau`, `-rel-threshold`). В `config.yaml` не вынесены.

| Параметр | Default | Флаг `context_eval` | Влияние |
|---|---|---|---|
| `stackdet.Params.Penalty` | `0.3` | `-penalty` | Штраф факту, чей стек целиком вне session ∪ названных в вопросе стеков. Ниже — сильнее давит «чужие» факты → меньше Leak, но риск задавить релевантный факт чужого стека. |
| `stackdet.Params.Bonus` | `1.5` | `-bonus` | Бонус факту, чей стек явно назван в вопросе (Named). Выше — сильнее поднимает «свой» факт на явный вопрос о стеке. |
| `stackdet.Params.Session` | `1.0` | `-session` | Бонус факту, чей стек целиком в session (когда вопрос стек не называет). Дефолт `1.0` = нейтрализован (M7b.5): сессионный бонус душил релевантные контентные факты с пустым стеком (no-stack-fact R@1 0.000). Выше — поднимает факты session-стека ценой recall no-stack-fact. |
| `ContentGateTau` | `0.15` | `-gate-tau` | content-only gate: минимальная доля IDF вопроса, покрытая телом факта; ниже — отброс. Выше — жёстче (меньше ложных, теряет recall). Отрицательное — выключить. |
| `RelativeThreshold` | `0.6` | `-rel-threshold` | relative threshold: Core ниже top1.Core × порог — отброс. Выше — агрессивнее отсекает хвост. Отрицательное — выключить. |

Порядок правил множителя и точная семантика — `stackdet/multiplier.go`
(комментарий над `Multiplier`). Замер влияния — `test/context_eval -slices`.

## Ограничения (v1)

Без embeddings/semantic retrieval, без приоритета фактов, без авто-сегментации
по markdown headings. Веса полей/sectionWeight — tuning defaults, меняются
только по результатам evaluation (train/holdout, метрики Recall@k и MRR).

Известные риски:

- **Ложная детекция стека на бытовых словах.** Семейство с коротким членом
  (например `go` у `golang`) может сработать на обычном слове; отсюда правило
  разметки: в `stack` только язык/платформа, бытовые слова в keywords/aliases —
  антипаттерн (см. format.md «Руководство по разметке»).
- **Малый датасет.** Один запрос сдвигает Recall@k/LeakRate на несколько
  пунктов; метрики — ориентир, а не точная мера. При калибровке сверяться по
  margin, а не по сотым долям.
- **Поведение при ошибке загрузки** зависит от `on_load_error` (fail/degraded)
  и `allow_legacy_fallback` (см. «Жизненный цикл»).
- **Нет стемминга/лемматизации**: `running` и `run` — разные токены; синонимия —
  только через явные `aliases`.

## Evaluation

Два набора. **Legacy set** (`train.json` 15 запросов, `holdout.json` 8) —
заморожен, это regression gate: новые изменения не должны ронять его метрики.
**v2-набор** (`eval_v2_train.json` 43, `eval_v2_holdout.json` 15) — вопросы с
тегами-срезами (`no-stack-fact`, `explicit-stack`, `service-collision`, …),
считает Recall@k/MRR/LeakRate/EmptyRate/FalseShowRate.

Метрики: `Recall@k` — доля кейсов, где хотя бы один relevant факт в top-k;
`MRR` — средний обратный ранг первого relevant; `LeakRate` — доля кейсов, где
в выдаче есть `must_not`-факт; `EmptyRate` — доля кейсов с пустой выдачей;
`FalseShowRate` — доля ложных подсказок (hints).

Реальный прогон 2026-10-18 19:25:27:

```text
go run ./test/context_eval -dir candidate_context -hints-dir hints \
  -train internal/context/testdata/train.json -holdout internal/context/testdata/holdout.json \
  -v2 train=internal/context/testdata/eval_v2_train.json,holdout=internal/context/testdata/eval_v2_holdout.json \
  -slices
```

| Набор | R@1 | R@3 | R@5 | MRR | Leak |
|---|---|---|---|---|---|
| legacy-train | 0.733 | 0.933 | 1.000 | 0.847 | 0.000 |
| legacy-hold | 0.875 | 1.000 | 1.000 | 0.917 | 0.000 |
| v2-train | 0.500 | 0.643 | 0.643 | 0.560 | 0.116 |
| v2-holdout | 0.429 | 0.429 | 0.429 | 0.429 | 0.067 |

Holdout (v2) — только финальная проверка, не для подбора порогов.

## Эксплуатация

Чек-лист перед интервью:

1. Выставить целевой стек: `interview.target_stack` (config.yaml) или
   `INTERVIEW_TARGET_STACK` (env). Пусто = штрафов нет (D2).
2. Убедиться, что legacy-ключ не задан (`CANDIDATE_CONTEXT_FILE` /
   `candidate_context_file` закомментирован), иначе в prompt уходит весь файл
   вместо fact-level retrieval.
3. Проверить строку в логе со стартовым стеком сессии.

Пересборка индекса (безусловная): `go run ./test/context_index -dir candidate_context`
(для hints — `-dir hints`).

Отладка вопроса: `go run ./test/ctx_probe -dir candidate_context -q "вопрос"`
(флаг `-scores` — сырой ranking до бюджета/множителей).

Прогон eval — команда выше в «Evaluation».

## Статус и планы

| Модуль | Статус |
|---|---|
| M0–M9 | реализованы (см. разделы выше) |
| M10 индикатор hints off | реализован как лог+notifier, без UI-индикатора |

Отложено: гибридный retrieval (embeddings), слияние инстансов
candidate_context/hints, UI-индикатор деградации.

## Глоссарий

| Термин | Смысл |
|---|---|
| fact | единица retrieval; граница — от маркера до следующего маркера |
| Raw | `field + section + phrase`, до множителя |
| Core | `(field + phrase) × mult`, без section-бонуса |
| Rank | `Core + section`, по нему ранжируется выдача |
| penalty set | session ∪ однозначные упоминания; вне него чужой стек штрафуется |
| bonus set | однозначно названный в вопросе стек (`Named`) |
| OutOfTarget | факт со стеком вне penalty set, оставшийся в выдаче |
| TokenizerVersion | версия семантики токенизатора, входит в freshness |
| legacy set | замороженные `train.json`/`holdout.json` для regression gate |
