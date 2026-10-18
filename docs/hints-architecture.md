# Hints — второй инстанс candidate-context retrieval

> Пакет `internal/context` (retrieval, scoring, index) описан в
> `docs/candidate-context-architecture.md`; формат данных — в
> `docs/candidate-context-format.md`. Здесь — архитектура применения
> инстанса **hints/**: как каталог готовых ответов подключается к pipeline,
> чем отличается от candidate context и как ведёт себя при ошибках.

## Зачем

Hints — заготовленные ответы на предсказуемые вопросы интервьюера. В отличие
от candidate context (справочные факты, из которых LLM собирает ответ),
hints-факт — это ГОТОВЫЙ ответ, который передаётся модели с пометкой
«использовать как есть» и в идеале должен быть произнесён дословно.

«candidate_context» — имя ФОРМАТА (пакет + структура каталога). В репо
каталогов этого формата два инстанса:

| Инстанс | Конфиг | Каталог | Назначение |
|---|---|---|---|
| Candidate context | `candidate_context.dir` (legacy: `candidate_context_file`) | `candidate_context/` — АКТИВЕН: 29 фактов, 7 секций (experience, sql-centric-approach, skills, alarm-architecture, summary, publications, education); в .gitignore | факты-справка про кандидата |
| Hints | `hints.dir` | `hints/` — 50 фактов, 1 секция (alarm-qa) | готовые ответы (QA платформы Alarm) |

Оба каталога исключены из git (.gitignore: `candidate_context/`, `candidate_context.md`) —
поэтому поиск по файлам (ripgrep) их не видит; проверять напрямую ls.

## Каталог hints/

```text
hints/
  manifest.json      # profile пуст; секция alarm-qa (tags: alarm, pharma, kafka, ftp, redis, s3, qa)
  index.json         # 50 фактов alarm-qa-01..50 (Java/Spring, платформа заказов аптек)
  sections/alarm-qa.md
```

Тот же формат, что и candidate context: manifest + sections с fact-markers +
генерируемый index (авто-пересборка при устаревании по SHA256). Формат
факта — см. `docs/candidate-context-format.md`.

## Подключение (pipeline)

```text
resolveCandidateContext (cfg.CandidateContext.Dir)          # pipeline.go
  → candidateContextFn(question) — факты по бюджету, prompt-ready

resolveHints (cfg.Hints.Dir)                                # pipeline.go:347
  → hintsFn(question) — тела топ-фактов, БЕЗ профиля и БЕЗ бюджета

if hintsFn != nil:
  candidateContextFn = q → appendHint(candidateContextFn(q), hintsFn(q))
```

Dispatcher вызывает единственную скомпонованную функцию и кладёт результат
в `AnswerRequest.CandidateContext` — как и без hints. Отдельного канала
для подсказок нет.

`appendHint` (pipeline.go:380): пустая подсказка → base без изменений;
иначе блок с заголовком `Suggested answer (use as is):` после фактов.

## Отличия от candidate context

| | Candidate context | Hints |
|---|---|---|
| Что возвращает | profile + факты по бюджету (Budgeter) | только тела фактов (join `\n`), без профиля, без Budgeter |
| Режимы | Dir (retrieval) или legacy-файл целиком | только Dir; legacy-режима нет |
| Ошибки загрузки | фатальны (если нет legacy fallback) | НЕ фатальны: `WARN` в sessLog, hints отключаются |
| Совместимость | — | требует fact-level режим candidate context; в legacy-режиме (`candidateContextFn == nil`) hints отключаются с WARN |
| Параметры | `max_tokens`, `max_profile_tokens`, `min_score`, `top_k` | `min_score`, `top_k` |

## Конфигурация

`hints` (config.yaml) / `HINTS_*` (.env):

| Параметр | Default (config.yaml) | Назначение |
|---|---|---|
| `dir` | `"hints"` | каталог формата candidate_context; `""` = hints выключены |
| `min_score` | `50` | строгий порог: ниже — подсказка не выводится вообще (заготовленный ответ нельзя показывать на слабом совпадении) |
| `top_k` | `1` | подсказка одна: несколько «готовых ответов» подряд противоречат друг другу |

Подбор порога: smoke-тест показал «свои вопросы ≥ 62, посторонние ≤ 36.5»,
порог 50 — с зазором к обеим сторонам.

## Поведение при ошибках

- `hints.dir == ""` — выключены, ветка compose не выполняется.
- Ошибка загрузки/инициализации (LoadCandidateContext, NewRetriever) —
  `WARN` в sessLog, `hintsFn == nil`, pipeline продолжает работу:
  вспомогательный каталог не должен ронять сессию.
- Candidate context в legacy-режиме — hints отключаются с WARN
  (retrieval по вопросу недоступен).

## Ограничения

- Hints не проходят через Budgeter: суммарный размер контекста ограничен
  только общими лимитами промпта (при `top_k: 1` и пороге 50 это не проблема).
- Profile hints-manifest игнорируется сознательно: подсказка — только тела.
- Стек-множители/effective target (план precision) будут применяться к обоим
  инстансам одинаково — hints использует тот же Retriever.
