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
  ]
}
```

* `profile` — всегда используется в system prompt.
* `global_aliases` — map `canonical term → []variants`; canonicalization
  выполняется tokenizer'ом при старте.
* `sections` — metadata для section-aware scoring. Целиком в prompt не
  попадают.

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
  "aliases": ["price engine", "market data"]
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

Отсутствие marker — ошибка builder. Текст между markers — тело факта;
из него builder строит `ContentTokens` (canonicalized).

## index.json

```json
{
  "version": 1,
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
      "content_tokens": ["pricing", "engine", ...]
    }
  ]
}
```

* `version` — версия формата (v1 = `1`).
* `manifest_sha256` — SHA256 содержимого `manifest.json`.
* `files` — metadata source-файлов (путь, размер, SHA256). Пути используют `/`.
* `facts` — список фактов. `start`/`end` — byte offsets (int64) тела факта.
  `keywords`, `aliases`, `content_tokens` — уже canonicalized.
  `title_tokens` в JSON не сохраняются (строятся при создании Retriever).

## Stale detection

Индекс устарел, если: изменился `manifest.json`, изменился размер или SHA256
source-файла, версия index несовместима, отсутствует ожидаемый source-файл.

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

`MaxTokens` (facts, default 2000) и `MaxProfileTokens` (profile, default 150) —
раздельные бюджеты. Facts берутся целиком в порядке score; обрезка — только
fallback для top-1 (UTF-8 safe).
