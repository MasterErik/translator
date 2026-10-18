# Candidate Context — стек-множители (M7b)

> Пакет `internal/context/stackdet`. Общий поток retrieval — в
> `docs/candidate-context-architecture.md`; формат данных — в
> `docs/candidate-context-format.md`.

## Зачем

Стек-множитель корректирует lexical score факта в зависимости от того,
совпадает ли стек факта со стеком вопроса/сессии. Цель — не подтягивать
php/go-факты на java-вопрос и поднимать факты, чей стек явно назван в вопросе.

Множитель применяется ТОЛЬКО к сопоставимой части score (без section-бонуса):

| Величина | Формула |
|---|---|
| `Raw` | `field + section + phrase` (до множителя) |
| `Core` | `(field + phrase) × mult` |
| `Rank` | `Core + section` |

`min_score` и абсолютный порог hints сравниваются с `Raw`, relative threshold —
с `Core`; множитель не может провести чужой факт через абсолютный порог (D7).

## Множества (Sets)

`Detect(questionTokens, sessionStack, families)` строит три непересекающихся
по смыслу множества стеков (`stackdet/types.go`):

| Множество | Смысл |
|---|---|
| `Penalty` | session ∪ однозначные упоминания в вопросе. Всё вне него — чужой стек, штрафуется. |
| `Named` | однозначно названные в вопросе стеки. Только они получают бонус. |
| `Exempt` | стеки, упомянутые лишь неоднозначно (например `spring` у `java`). Освобождены от штрафа, но без бонуса. |

Пустая сессия и вопрос без упоминаний стеков → `Penalty` пусто → множитель
`1.0` (штрафов нет). Сессия задаётся `interview.target_stack` / env
`INTERVIEW_TARGET_STACK` (см. architecture, конфигурация).

## Правила Multiplier

`stackdet.Multiplier(factStack, sets, params)` — порядок правил (первое
сработавшее побеждает), `stackdet/multiplier.go`:

1. `factStack` пуст → `1.0`;
2. `Penalty` пусто → `1.0`;
3. `Named` непусто и `factStack ∩ Named ≠ ∅` → `Bonus`;
4. `Named` пусто и `factStack ⊆ Penalty` → `Session` (при `Session=1.0` — `1.0` без reason);
5. частичное пересечение с `Penalty ∪ Exempt` → `1.0`;
6. иначе → `Penalty`.

`reason` непуст при любом `mult ≠ 1.0`; при `1.0` — пустая строка.

Правило 3 приоритетнее правила 4: стек, торчащий в факте из сессии, не
получает сессионный бонус, если вопрос называет другой стек (иначе java-факт
с общим словом обогнал бы php-факт на явный php-вопрос).

## Коэффициенты (Params)

`stackdet/types.go`, `DefaultParams()`:

| Коэффициент | Default | Смысл |
|---|---|---|
| `Penalty` | `0.3` | штраф факту, чей стек целиком вне `Penalty` |
| `Bonus` | `1.5` | бонус факту, чей стек назван в вопросе (`Named`) |
| `Session` | `1.0` | бонус факту, чей стек целиком в session; `1.0` = нейтрален |

Калибровка (M7b.5, 2026-10-18): `Session` 1.25 → **1.0**. Сессионный бонус
душил релевантные факты с пустым стеком (`no-stack-fact` R@1 0.000 при 1.25);
`1.0` восстанавливает 0.286 и поднимает v2-train R@1 0.429 → 0.500 ценой
Leak 0.093 → 0.116 (+1 кейс). `Penalty` и `Bonus` не менялись: sweep показал,
что `Penalty` 0.1–0.3 не влияет на Leak/recall. Пересмотр — после расширения
факт-базы.

`Params{}` (нулевая структура) трактуется как дефолт через `effective()`:
каждое нулевое поле подставляет дефолт соответствующего коэффициента (поэтому
нельзя «выключить» коэффициент, передав `0` — для нейтрализации используется
`Session = 1.0`).

## OutOfTarget

`stackdet.IsOutOfTarget(factStack, sets) bool` — стек факта целиком вне
`Penalty ∪ Exempt` (факт чужой). Для reference-фактов — пометка в выдаче
(`OUT OF TARGET STACK — use only if the question is directly about it`), для
hints — запрет на выдачу готового ответа (D8). Пустой `factStack` или пустой
`Penalty` → `false`.

## Примеры (вымышленные)

Сессия `java`, вопрос без упоминаний стеков, `Session=1.0`, `Bonus=1.5`,
`Penalty=0.3`:

| factStack | Правило | mult | reason |
|---|---|---|---|
| `[]` | 1 | 1.0 | — |
| `["java"]` | 4 (Session=1.0) | 1.0 | — |
| `["php"]` | 6 | 0.3 | `penalty: stack [php] ∉ {java}` |
| `["java","php"]` | 5 (частичное) | 1.0 | — |

Вопрос `"php experience"` на той же сессии (`Named={php}`, `Penalty={java,php}`):

| factStack | Правило | mult | reason |
|---|---|---|---|
| `["php"]` | 3 | 1.5 | `bonus: named php` |
| `["java"]` | 5 (частичное) | 1.0 | — |
| `["golang"]` | 6 | 0.3 | `penalty: stack [golang] ∉ {java, php}` |

## Калибровка (CLI)

Коэффициенты переопределяются флагами `test/context_eval` (`-penalty`,
`-bonus`, `-session`) и `ContextFnConfig.StackParams`; дефолты — в
`stackdet.DefaultParams()`. В `config.yaml` коэффициенты не вынесены.

```
go run ./test/context_eval -dir candidate_context -hints-dir hints \
  -v2 train=internal/context/testdata/eval_v2_train.json,holdout=internal/context/testdata/eval_v2_holdout.json \
  -slices -session 1.0
```

## Ссылки

- `stackdet/multiplier.go` — `Multiplier`, `IsOutOfTarget`, helpers.
- `stackdet/detect.go` — `Detect` (построение `Sets`).
- `stackdet/types.go` — `Params`, `Family`, `Sets`, `DefaultParams`.
- `internal/context/retrieval.go` — применение множителя (`stackMultiplier`, `factParts`).
