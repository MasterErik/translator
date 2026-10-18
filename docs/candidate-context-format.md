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
  её член (нормализуется к имени семьи при сборке индекса).
* `period` — период занятости, `^\d{4}(-\d{4}|-present)?$`.
* `weight` — значимость факта, enum: `primary` | `secondary` | `background`.
* `lint_ignore` — подавляет lint-предупреждение «токен tech_families в тексте
  при пустом `stack`»; в индекс не переносится.

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

* токен семейства в `title` или теле факта при пустом `stack` — вероятно,
  факт размечен неполно, и retrieval не сможет ни дать ему бонус, ни
  оштрафовать его; подавляется явным `lint_ignore`.

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

## Scoring (v1)

`fact score + section bonus + phrase bonus`. Section bonus и phrase bonus
применяются только при `fact score > 0` — если fact score равен 0, факт
отбрасывается сразу (section metadata не создаёт результат сама по себе).

Field weights (initial tuning defaults): keywords 5, title 4, aliases 3,
content 2. Section fields: tags 3, summary 2; section score умножается на
`sectionWeight` (default 0.3). Phrase bonus: title +3, content +1, cumulative
max +6. `IDF(term) = log((N+1)/(df+1)) + 1`; `df` — по фактам, не по
вхождениям. `fact score == 0` не считается результатом.

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
при непустой матрице) → факты.
