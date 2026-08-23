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
| `tokenize.go` | `Tokenizer`: Normalize→Tokenize→RemoveStopWords→Canonicalize; alias validation |
| `builder.go` | `BuildIndex` (markers→facts, offsets, SHA256), `SaveIndex` (temp+rename) |
| `score.go` | Веса полей, IDF, phrase/bigram matching |
| `retrieval.go` | `Retriever`: dfMap, title/section tokens, ranking, точечное чтение тел |
| `budget.go` | `Budgeter`: раздельные бюджеты profile/facts, UTF-8 safe обрезка |
| `eval.go` | `EvalQuery`, `EvalDataset`, `LoadEvalDataset`, `EvalMetrics`, `Evaluate` (Recall@k/MRR) |

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
размера/SHA256 source-файла, несовместимой версии index, отсутствии ожидаемого
source-файла. Source-файлы не меняются в runtime — file watcher/locking не нужны.

При ошибке пересборки pipeline делает legacy fallback (`CandidateContextFile`),
если он задан; иначе startup завершается ошибкой.

## Поток retrieval (на каждый вопрос)

```text
вопрос
  → Tokenizer.Process(question)                       # canonical tokens, без stop words
  → section score (tags 3 × IDF + summary 2 × IDF) × sectionWeight(0.3)
  → fact score = keywords(5) + title(4) + aliases(3) + content(2)   [× IDF]
  → fact score == 0 → отброс                         # section metadata не создаёт результат
  → + section bonus + phrase bonus                   # только при fact score > 0; phrase: title +3 / content +1, cap +6
  → score < minScore → отброс
  → сортировка score DESC, tie-break по FactID ASC
  → topK → точечное чтение тел через io.NewSectionReader
  → Budgeter: profile ≤ MaxProfileTokens, факты целиком ≤ MaxTokens
  → CandidateContext.Render() → AnswerRequest.CandidateContext (prompt-ready)
```

`IDF(term) = log((N+1)/(df+1)) + 1`; `df` считается по фактам (каждый факт
максимум один раз на термин), TF не учитывается. Raw source файлы НЕ читаются
для ranking — только после ranking, для отобранных фактов.

## Интеграция

- `pipeline.New`: при `CandidateContext.Dir != ""` → `LoadCandidateContext` →
  `NewRetriever` + `NewBudgeter` → `candidateContextFn(question)` в dispatcher.
  При `Dir == ""` — legacy путь (`CandidateContextFile` читается целиком).
  Приоритет `Dir` над `CandidateContextFile`.
- `dispatcher.generateAnswers`: если `candidateContextFn` задан — вызывает его
  с текущим вопросом и кладёт результат (уже prompt-ready строка) в
  `AnswerRequest.CandidateContext`.
- `buildSystemPrompt` не меняется: получает уже собранный prompt-ready контекст.

## Конфигурация

`candidate_context` (config.yaml, lowercase) / `CANDIDATE_CONTEXT_*` (env):

| Параметр | Default | Назначение |
|---|---|---|
| `dir` / `CANDIDATE_CONTEXT_DIR` | `""` | путь к каталогу candidate_context; `""` = legacy |
| `max_tokens` / `CANDIDATE_CONTEXT_MAX_TOKENS` | `2000` | бюджет фактов |
| `max_profile_tokens` / `CANDIDATE_CONTEXT_MAX_PROFILE_TOKENS` | `150` | бюджет профиля |
| `min_score` / `CANDIDATE_CONTEXT_MIN_SCORE` | `0` | минимальный lexical score (0 = без порога) |
| `top_k` / `CANDIDATE_CONTEXT_TOP_K` | `5` | максимум фактов |

`Retriever` не знает о budget; `Budgeter` не знает о `min_score` и tokenizer.

## Ограничения (v1)

Без embeddings/semantic retrieval, без приоритета фактов, без авто-сегментации
по markdown headings. Веса полей/sectionWeight — tuning defaults, меняются
только по результатам evaluation (train/holdout, метрики Recall@k и MRR).

## Evaluation (baseline)

Datasets: `internal/context/testdata/train.json` (15 queries) и
`holdout.json` (8 queries) — вопросы интервью → relevant FactID.
Прогон: `go run ./test/context_eval --dir candidate_context --train ... --holdout ...`.

| Dataset | Recall@1 | Recall@3 | Recall@5 | MRR |
|---|---|---|---|---|
| train | 0.600 | 0.867 | 0.933 | 0.739 |
| holdout | 0.500 | 1.000 | 1.000 | 0.729 |

Recall@5 ≈ 90%+ (ориентир из плана), MRR ~0.73. Это baseline v1; field weights,
aliases и ranking корректируются только по результатам evaluation.
