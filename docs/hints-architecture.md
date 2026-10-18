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
| Candidate context | `candidate_context.dir` (legacy: `candidate_context_file`) | `candidate_context/` — АКТИВЕН: 31 факт, 7 секций (experience, sql-centric-approach, skills, alarm-architecture, summary, publications, education); в .gitignore | факты-справка про кандидата |
| Hints | `hints.dir` | `hints/` — 50 фактов, 1 секция (alarm-qa) | готовые ответы (QA платформы Alarm) |

`candidate_context/` и `candidate_context.md` исключены из git (.gitignore) —
поэтому поиск по файлам (ripgrep) их не видит; проверять напрямую ls. Каталог
`hints/` в .gitignore не значится и лежит в репозитории.

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
BuildContextFn(cfg)                      # internal/context/contextfn.go
  hints и candidate context загружаются вместе (M0), один контракт для прода и eval
  → fn(question, SessionContext{TargetStack}) →
      Budgeter(факты) → Render → AppendHint(…, hints.Retrieve(question, session))
```

`pipeline.New` (pipeline.go) передаёт в `BuildContextFn` `HintsDir/HintsTopK/HintsMinScore`
и создаёт сессию один раз: `SessionContext{TargetStack: cfg.Interview.TargetStack}`
(`interview.target_stack` в config.yaml / env `INTERVIEW_TARGET_STACK`).

Одна и та же сессия используется для candidate context и для hints: `hintSource.Retrieve`
вызывает `Retriever.RetrieveWith(question, RetrieveOptions{Session: session})` —
тот же Retriever, те же `stackdet`-множители и `IsOutOfTarget`, что у candidate context.

Подсказки встраиваются в тот же результат сборки (`ContextResult.Text` —
`AppendHint(base, hintText)`, заголовок `Suggested answer (use as is):`; пустая
подсказка → base без изменений). Диспетчер вызывает единственную скомпонованную
функцию и кладёт результат в `AnswerRequest.CandidateContext` — отдельного канала
для подсказок нет. `ContextResult.HintIDs` заполняется ID показанных подсказок
(для eval-контура); `ContextResult.HintsDisabled` — флаг M10.

## Отличия от candidate context

| | Candidate context | Hints |
|---|---|---|
| Что возвращает | profile + matrix + факты по бюджету (Budgeter) | только тела фактов (join `\n`), без профиля и matrix, без Budgeter |
| Режимы | Dir (retrieval) или legacy-файл целиком | только Dir; legacy-режима нет |
| Ошибки загрузки | фатальны (если нет legacy fallback / `on_load_error=degraded`) | НЕ фатальны: `ERROR` в sessLog, hints отключаются |
| Совместимость | — | требует fact-level режим candidate context; при legacy-режиме hints отключаются с WARN (см. «Отбор подсказок») |
| Параметры | `max_tokens`, `max_profile_tokens`, `max_matrix_tokens`, `min_score`, `top_k` | `min_score`, `top_k` |

## Отбор подсказок (retrieval + стек)

Hints использует тот же `Retriever`, что и candidate context, но с отдельными
опциями — `newHintSource` (`internal/context/contextfn.go`):

| Опция `RetrieverOptions` | Значение у hints | Следствие |
|---|---|---|
| `MinScore` | `hints.min_score` | абсолютный порог, сравнивается с **Raw** |
| `ContentGateTau` | `-1` (выключен) | content-only gate не отсекает подсказку |
| `RelativeThreshold` | `-1` (выключен) | нет порога «доля от лучшего core» |
| `DropOutOfTarget` | `true` | факт вне целевого стека не выдаётся вообще |
| `TopK` | `hints.top_k` | срез после фильтров |

Порядок вердиктов в `Retriever.collect` (`internal/context/retrieval.go`):
`out_of_target` → `content_gate` → `min_score` → relative threshold → topK.

**Сравнение с Raw.** Абсолютный порог сравнивается с `Raw = field + section + phrase`
— значением до стек-множителя. Множитель не проводит чужой факт через порог
(D7): бонус 1.5 не помогает факту с низким Raw, штраф 0.3 не «убивает» факт,
у которого Raw высокий. Тот же Raw сравнивается и с `candidate_context.min_score`.
`Rank = (field + phrase) × mult + section` используется только для сортировки и
relative threshold (последний у hints выключен).

**Стек-множители применяются.** Сессия целиком попадает в `stackdet.Detect`
штрафное множество (`Penalty`), однозначные упоминания семейств в вопросе
расширяют `Penalty`, неоднозначные — `Exempt`. Для hints это влияет на `Rank`
(сортировку внутри top-K), но не на проход абсолютного порога.

**Факт вне целевого стека не выдаётся вообще** (`stackdet.IsOutOfTarget`, D8):
если стек факта не пересекается ни с `Penalty`, ни с `Exempt`, кандидат
получает `Gate = "out_of_target: <причина множителя>"` и не попадает в выдачу.
Пустой стек факта, пустой `Penalty` (в т.ч. незаданный `interview.target_stack`)
или косвенные неопределённости дают `false` — «чуждость» не устанавливается,
подсказка остаётся доступной. В отличие от candidate context, где такой факт
рендерится с пометкой `OUT OF TARGET STACK — …`, у hints пометки нет: он
просто отсутствует.

## Конфигурация

`hints` (config.yaml) / `HINTS_DIR`, `HINTS_MIN_SCORE`, `HINTS_TOP_K` (.env):

| Параметр | Default (applyDefaults) | config.yaml | Назначение |
|---|---|---|---|
| `dir` | `""` | `"hints"` | каталог формата candidate_context; `""` = hints выключены |
| `min_score` | `0.0` | `50` | строгий порог по Raw: ниже — подсказка не выводится вообще (заготовленный ответ нельзя показывать на слабом совпадении) |
| `top_k` | `1` | `1` | подсказка одна: несколько «готовых ответов» подряд противоречат друг другу |

`applyDefaults` подставляет default только вместо `0`, а `0.0` — и есть нулевое
значение, поэтому незаданный `min_score` в конфиге остаётся `0.0` (порог
отключён). Рабочее значение в проде — **50** из `config.yaml`;
`context_eval -hints-min-score` тоже имеет default **50**.

Калибровка порога 50 (`config.yaml`, комментарий к `min_score`): smoke-тест
показал «свои вопросы ≥ 62, посторонние ≤ 36.5» — порог 50 лежит ровно между
ними. Полностью совпадает со сравнением по Raw.

## Поведение при ошибках

- `hints.dir == ""` — hints не загружаются, ветка подсказок не выполняется,
  `HintsDisabled` остаётся `false` (индикатор не срабатывает).
- Ошибка загрузки/инициализации (`LoadCandidateContext`, `NewRetrieverWithOptions`,
  `NewTokenizer`) —
  `ERROR: hints недоступны (<err>) — подсказки отключены` в sessLog (`cfg.Logf`),
  `hintsDisabled = true`, pipeline продолжает работу: вспомогательный каталог
  не должен ронять сессию.
- Legacy-режим candidate context (`candidate_context.dir == ""`, есть legacy-файл) —
  hints требуют retrieval по вопросу: `WARN: hints задан, но candidate context
  работает в legacy-режиме — hints отключены`, `hintsDisabled = true`.
- Fallback на legacy-файл после сбоя fact-level загрузки (`allow_legacy_fallback`) —
  hints тоже отключаются (`hintsDisabled = true`).
- `on_load_error=degraded` при сбое загрузки candidate context — возвращается
  degraded-функция с явной пометкой; в этой ветке подсказок нет
  (`hintsDisabled` в результат не выставляется — индикатор M10 опирается на
  сбой самого каталога hints или на legacy-режим).
- Не сконфигурировано ничего (`candidate_context.dir == ""` и нет legacy-файла) —
  сессия с пустым контекстом; hints при этом требуют каталог candidate context.

## Индикатор M10

`ContextResult.HintsDisabled` = «каталог hints задан (`hints.dir != ""`), но
подсказки отключены на всю сессию». `pipeline.New` после сборки определяет
причину один раз:

- `detectHintsDisabled(cfg, contextFn)` — если `cfg.Hints.Dir == ""` → пусто;
  иначе нейтральная проба `hintsProbeQuestion` («hints status probe», ничего
  не находит — I/O нет); `HintsDisabled == false` → пусто;
- причины: `candidate context работает в legacy-режиме — retrieval по вопросу
  недоступен`; `candidate context не сконфигурирован — подсказки требуют
  fact-level retrieval`; `недоступен каталог <dir> (см. WARN/ERROR выше в логе сессии)`.
- `reportHintsDisabled(sessLog, cfg.HintsDisabledNotifier, reason)` пишет три вещи:
  `WARN: hints отключены: <причина>; интервью продолжается без подсказок` в sessLog
  (`LogDebug`), `slog.Warn(..., "reason", reason)` и вызывает необязательный
  `Config.HintsDisabledNotifier func(reason string)` (nil-safe).

UI-индикатора нет — потребитель (UI/телеметрия) подписывается через notifier.

## Ограничения

- Hints не проходят через Budgeter: в prompt уходит `join("\n")` тел фактов без
  профиля, matrix и токен-лимитов `max_tokens/max_profile_tokens/max_matrix_tokens`;
  суммарный размер ограничен только общими лимитами промпта (при `top_k: 1` и
  пороге 50 это не проблема).
- Profile и `technical_matrix` hints-manifest игнорируются сознательно: подсказка —
  только тела.
- Стек-множители и `DropOutOfTarget` применяются к hints так же, как к candidate
  context (тот же `Retriever`, та же сессия `interview.target_stack`).
- Стоимость ложных подсказок измеряется метрикой `FalseShowRate` в eval:
  `FalseShows / HintShows` по `ContextResult.HintIDs` (кейс считается ложным,
  если ни один показанный hint не входит в `relevant`); печатается
  `context_eval` колонкой `FalseShow` (по срезам — так же).
