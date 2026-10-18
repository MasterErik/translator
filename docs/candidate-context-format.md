# Candidate Context — формат и retrieval

> Package: `internal/context` (package name `candidatecontext`).
> Термин: **fact-level lexical retrieval with section-aware scoring**.

## Зачем

Полная база кандидата никогда не передаётся в prompt. В system prompt
попадает только компактный профиль + точечно релевантные факты по текущему
вопросу. Retrieval — локальный, lexical, без embeddings/vector DB/внешних
сервисов. Только stdlib.

## Структура каталога

```text
candidate_context/
  manifest.json      # profile + global_aliases + section metadata
  index.json         # runtime retrieval index (строится/пересобирается автоматически)
  sections/          # source data (markdown с fact markers)
    <name>.md
```

`manifest.json` и `sections/*.md` — source data (редактируются вручную).
`index.json` — генерируемый файл; пересобирается автоматически при
устаревании (изменение manifest или source-файлов, несовместимая версия).

## manifest.json

```json
{
  "profile": "Erik Ivanov — Staff Software Engineer ...",
  "global_aliases": {
    "kubernetes": ["k8s", "kube"],
    "postgresql": ["postgres", "pg", "psql"]
  },
  "sections": [
    {"id": "experience", "title": "Опыт", "summary": "...", "tags": ["java", "kafka"]}
  ],
  "tech_families": [
    {"name": "golang", "display": "Go", "members": ["golang"], "ambiguous": ["go"]}
  ],
  "technical_matrix": "stack | period\ngolang | 2021-present"
}
```

* `profile` — всегда используется в system prompt.
* `global_aliases` — map `canonical term → []variants`; canonicalization
  выполняется tokenizer'ом при старте.
* `sections` — metadata для section-aware scoring. Целиком в prompt не
  попадают.
* `tech_families` — белый список семейств технологий для определения стека
  (`internal/context/stackdet`): `name` — канонический ключ (`golang`, не `go`),
  `display` — метка для prompt, `members` — члены семьи, `ambiguous` — члены,
  которые нельзя считать однозначным упоминанием вопроса. Он же служит
  источником допустимых значений `Fact.stack` при валидации.
* `technical_matrix` — компактная таблица стек × период. Рендерится отдельным
  блоком prompt'а (`Technical matrix:`) сразу после профиля и со своим бюджетом
  `max_matrix_tokens` (default 100): бюджет профиля (150) обрезал бы её хвост.

## sections/*.md и fact markers

Каждая секция — один markdown-файл. Факты выделяются декларативными
markers. Граница факта — от текущего marker до следующего marker либо до
конца файла. Автоматическая сегментация по markdown headings в v1 не
поддерживается.

Marker — HTML-комментарий с JSON:

```markdown
<!-- fact
{
  "id": "java-head-pricing",
  "section": "experience",
  "title": "Pricing Engine & Market Intelligence",
  "keywords": ["pricing", "kafka", "postgresql", "clickhouse"],
  "aliases": ["price engine", "market data"],
  "stack": ["java"],
  "period": "2021-2024",
  "weight": "primary",
  "lint_ignore": "осознанное исключение"
}
-->
Pricing Engine: ...
```

Атрибуты marker:

* `id` — уникальный строковый идентификатор факта (не автоинкремент).
* `section` — ID секции из manifest.json (`Fact.Section` обязан ссылаться на
  существующий `Manifest.Sections[].ID`).
* `title` — человекочитаемый заголовок факта.
* `keywords` — термины для term matching.
* `aliases` — синонимы факта.
* `stack` — стеки факта: имя семьи из `tech_families` (`golang`, а не `go`) либо
  её член (нормализуется к имени семьи при сборке индекса). По D4 в `stack`
  попадают только языки/платформы (whitelist: `java`, `golang`, `php`, `python`,
  `typescript`, `vue`, `delphi`); БД/инструменты (kafka, postgresql, …) остаются
  в `keywords` и в `stack` не входят.
* `period` — период занятости, `^\d{4}(-\d{4}|-present)?$`.
* `weight` — значимость факта, enum: `primary` | `secondary` | `background`.
* `lint_ignore` — подавляет lint-предупреждение «токен языка/платформы в тексте
  при пустом `stack`» (whitelist D4); в индекс не переносится.

Отсутствие marker — ошибка builder. Текст между markers — тело факта;
из него builder строит `ContentTokens` (canonicalized).

## Валидация и lint

Разбор строгий (`DisallowUnknownFields`) и для маркеров, и для `manifest.json`:
неизвестное поле — ошибка. Опечатка (`weights`, `tech_familites`) иначе молча
игнорировалась бы, а данные выглядели бы корректными.

Ошибка сборки (индекс не создаётся):

* `stack` содержит термин, не описанный ни в одной `tech_families`;
* `weight` вне enum; `period` не по regex;
* дубликат `id` факта или `id` секции; `section` факта отсутствует в manifest;
* в таблице семей: пустой `name`, дубликат семьи, пустой член, токен,
  заявленный в двух семьях.

Нормализация: `stack` каждого факта канонизируется тем же tokenizer'ом, а затем
приводится к `Family.Name` — член семьи и неоднозначный член дают имя семьи
(`kotlin` → `java`, `mssql` → `microsoft sql server` → `mssql`). Стек факта и
стек вопроса сравниваются по одному ключу.

Lint (сборка продолжается, предупреждение пишется в лог/консоль):

* токен языка/платформы (whitelist D4: `java`, `golang`, `php`, `python`,
  `typescript`, `vue`, `delphi`) в `title` или теле факта при пустом `stack` —
  вероятно, факт размечен неполно, и retrieval не сможет ни дать ему бонус, ни
  оштрафовать его; подавляется явным `lint_ignore`. Токены БД/инструментов
  (kafka, postgresql, …) и framework-токены (hibernate, mybatis, …)
  предупреждений не дают — по D4 они в `stack` не входят.

## index.json

```json
{
  "version": 2,
  "tokenizer_version": "3",
  "manifest_sha256": "<hex>",
  "files": [
    {"path": "sections/experience.md", "size": 1234, "sha256": "<hex>"}
  ],
  "facts": [
    {
      "id": "java-head-pricing",
      "section": "experience",
      "file": "sections/experience.md",
      "start": 120,
      "end": 480,
      "title": "Pricing Engine & Market Intelligence",
      "keywords": ["pricing", "kafka"],
      "aliases": ["price engine"],
      "content_tokens": ["pricing", "engine", ...],
      "stack": ["java"],
      "period": "2021-2024",
      "weight": "primary"
    }
  ]
}
```

* `version` — версия формата (`IndexVersion`; v2 = `2`). Несовместимая версия
  приводит к автоматической пересборке индекса.
* `tokenizer_version` — версия семантики токенизатора (`TokenizerVersion`).
  Меняется при любом изменении нормализации, подстановок, сплита токенов,
  стоп-слов или канонизации; **отсутствующая или чужая версия = индекс stale**
  (иначе старый индекс молча остался бы «свежим» с токенами старой семантики).
* `manifest_sha256` — SHA256 содержимого `manifest.json`.
* `files` — metadata source-файлов (путь, размер, SHA256). Пути используют `/`.
* `facts` — список фактов. `start`/`end` — byte offsets (int64) тела факта.
  `keywords`, `aliases`, `content_tokens`, `stack` — уже canonicalized.
  `title_tokens` в JSON не сохраняются (строятся при создании Retriever).
  `stack`/`period`/`weight` опускаются в JSON, если пусты.

## Stale detection

Индекс устарел, если: изменился `manifest.json`, изменился размер или SHA256
source-файла, версия index несовместима, `tokenizer_version` не совпадает
(включая отсутствие), отсутствует ожидаемый source-файл.

## Токенизация

`Tokenizer.Process` — порядок стадий (зафиксирован тестом `TestProcessOrder`):
`Normalize → Substitute → Tokenize → RemoveStopWords → Canonicalize`.
Подстановка видит сырую строку (до edge-trim и стоп-слов), канонизация — после
подстановки. Вопрос и факты проходят один и тот же пайплайн (симметрия).

Правила и примеры (из golden-теста `tokenize_test.go`, `tokenGolden`):

| Вход | Токены |
|---|---|
| `PHP/Laravel` | `php`, `laravel` |
| `TCP/IP` | `tcp`, `ip` |
| `R&D` | `r&d` |
| `low-latency` | `low-latency`, `low`, `latency` |
| `C++` | `cpp` |
| `C#` | `csharp` |
| `.NET Core` | `dotnet`, `core` |
| `Go microservices` | `golang`, `microservices` |
| `go live` | `live` (go без контекста — стоп-слово) |
| `the and in` | — (стоп-слова) |
| `a | b — c & d` | `b`, `c`, `d` |

Правила: сплит по `/` и `\`; дефис даёт целое + части; `&` внутри слова
сохраняется; спецтермины (`c++`→`cpp`, `c#`→`csharp`, `.net`→`dotnet`)
подставляются по точному совпадению и не затрагивают составные имена
(`asp.net`, `node.js`, `c++20`); `go` → `golang` только в контексте языка
(`go language`, `go developer`, `with go`, …), deny-list фраз (`go live`,
`go ahead`, `go through`) остаются; стоп-слова (en+ru) удаляются; глобальные
aliases канонизируются (`k8s`→`kubernetes`, `postgres`→`postgresql`).

Любое изменение этих правил = bump `TokenizerVersion` (иначе старые индексы
молча останутся «свежими» с токенами старой семантики).

## Scoring (v1)

`fact score + section bonus + phrase bonus`. Section bonus и phrase bonus
применяются только при `fact score > 0` — если fact score равен 0, факт
отбрасывается сразу (section metadata не создаёт результат сама по себе).

Field weights (initial tuning defaults): keywords 5, title 4, aliases 3,
content 2. Section fields: tags 3, summary 2; section score умножается на
`sectionWeight` (default 0.3). Phrase bonus: title +3, content +1, cumulative
max +6. `IDF(term) = log((N+1)/(df+1)) + 1`; `df` — по фактам, не по
вхождениям. `fact score == 0` не считается результатом.

Поверх lexical score — стек-множитель (M7b): `Core = (field + phrase) × mult`,
`Rank = Core + section`; пороги и коэффициенты множителя — в
`docs/candidate-context-architecture.md` («Регулируемые параметры скоринга»).

## Руководство по разметке фактов

Правила (нарушение ломает retrieval, а не только эстетику):

1. **Один факт — один стек.** Смешанный `stack` (`["java","php"]`) делает
   множитель бесполезным: факт не штрафуется (частичное пересечение) и не
   бонусуется (не подмножество). Разносите по фактам.
2. **Нейтральная часть отдельно от технической.** Текст про «hardest problem»
   без технологий — отдельный факт без `stack`; технический опыт — факт со
   `stack`.
3. **В `stack` только язык/платформа** (whitelist D4: java, golang, php,
   python, typescript, vue, delphi). БД/инструменты (kafka, postgresql, …) —
   в `keywords`, не в `stack`: иначе факт получает ложный штраф «чужой стек»
   на сессии другого языка.
4. **Бытовые слова — антипаттерн.** `current`, `present`, `service`, `web`,
   `performance`, `go` (вне контекста) в keywords/aliases дают вес поля × IDF
   и поднимают факт на нерелевантных вопросах. Такие слова — не retrieval-сигнал,
   а шум.
5. **`lint_ignore`** — только осознанно: когда язык/платформа упомянута в
   тексте, но факт сознательно оставлен без `stack`.

Примеры (вымышленные):

Плохо (бытовые слова + инструменты в stack):
```markdown
<!-- fact {"id":"exp-x","section":"experience","keywords":["service","current","performance"],"stack":["java","kafka"]} -->
```

Хорошо:
```markdown
<!-- fact
{"id":"exp-x","section":"experience","title":"Order Processing","keywords":["order processing","kafka","exactly-once"],"stack":["java"]}
-->
```

Нейтральный факт без стека:
```markdown
<!-- fact
{"id":"skill-x","section":"skills","keywords":["estimation","architecture initiative"],"title":"Estimating architecture initiatives"}
-->
```

## Budget

`MaxTokens` (facts, default 2000), `MaxProfileTokens` (profile, default 150) и
`MaxMatrixTokens` (`technical_matrix`, default 100) — раздельные бюджеты.
Facts берутся целиком в порядке score; обрезка — только fallback для top-1
(UTF-8 safe). Матрица обрезается своим бюджетом независимо от профиля.

**Ноль — не «без лимита».** Нулевой бюджет означает «поле не задано»: вместо
тихой потери контекста подставляется дефолт, и на старте пишется WARN с
подставленным значением (в v1 нулевой `MaxTokens` отдавал усечённый top-1 без
единого предупреждения). Отрицательный бюджет — ошибка старта.

Порядок блоков prompt'а: `Candidate profile:` → `Technical matrix:` (только
при непустой матрице) → факты, каждый с меткой `[Java · 2018–2024 · primary]`;
пустая выдача → строка «No relevant facts found…». Детали render/prompt —
`docs/candidate-context-architecture.md` («Render и prompt (M9)»).

## Миграция v1 → v2

Формат v2 (текущий `IndexVersion = 2`) добавил к v1: `tech_families` и
`technical_matrix` в `manifest.json`; поля `stack`/`period`/`weight`/`lint_ignore`
в маркере и `Fact`; `tokenizer_version` в `index.json`. Правила разметки v2:
`stack` — только язык/платформа (D4), бытовые слова — антипаттерн.

Пересборка индекса — безусловная:
`go run ./test/context_index -dir candidate_context` (для hints — `-dir hints`).
Автоматически индекс пересобирается при stale (см. «Stale detection»).
