# Макет нового UI — 4 зоны

```
┌──────────────────────────────────────────────────────────────┐
│  РЕЧЬ (оригинал)                                             │
│  I have five years of experience in software development     │  ← 2 строки, серый
│  and I worked with Kubernetes and Docker...                  │     обновляется на каждый interim
│                                                              │     после EndOfTurn → финальный текст
├──────────────────────────────────────────────────────────────┤
│  ПЕРЕВОД                                                     │
│  У меня пять лет опыта в разработке программного             │  ← 5 строк, жёлтый (pending)
│  обеспечения, и я работал с Kubernetes и Docker...           │     → зелёный (streaming)
│                                                              │     → белый (done)
│                                                              │     заменяется при новом переводе
│                                                              │
├──────────────────────────────────────────────────────────────┤
│  ПОДСКАЗКИ (если вопрос)                                     │
│  1. EN: Mention Kubernetes exp | RU: Вспомни про опыт с K8s  │  ← до 3 строк, зелёный
│  2. EN: Talk about Docker...    | RU: Расскажи про Docker...  │     появляются асинхронно
│  3. EN: Reference CI/CD...      | RU: Упомяни CI/CD...       │
├──────────────────────────────────────────────────────────────┤
│  ИСТОРИЯ (оригиналы)                                  [скролл]│
│  1. I have five years of experience in software development   │  ← накапливается
│  2. Well, I think I like this supermarket because...          │     EndOfTurn → append
│  3. Can you explain how Kubernetes handles service discovery? │     скроллируемая
│  4. I worked at Google for three years as a senior engineer   │
│  5. We used microservices architecture with event sourcing    │
│  6. The biggest challenge was scaling the database layer      │
│  ...                                                          │
└──────────────────────────────────────────────────────────────┘
```

## Размеры

| Параметр | Было | Стало |
|----------|------|-------|
| Ширина | 800 | 800 |
| Высота | 400 | 650 |
| Речь | 2 строки | 2 строки |
| Перевод | скролл 10 строк | фиксировано 5 строк |
| Подсказки | динамически | до 3 строк |
| История | нет | скролл, остаток высоты |

## Поток данных

```
Interim (Update) → зона РЕЧЬ (обновить текст)
                 → асинхронный перевод → зона ПЕРЕВОД (streaming)

EndOfTurn        → зона РЕЧЬ (финализировать)
                 → append в зону ИСТОРИЯ
                 → стриминг-перевод → зона ПЕРЕВОД (done)
                 → if вопрос → асинхронно зона ПОДСКАЗКИ

Новый EndOfTurn  → зона РЕЧЬ (новый текст)
                 → зона ПЕРЕВОД (pending → streaming → done)
                 → зона ПОДСКАЗКИ (заменить)
                 → ИСТОРИЯ (добавить строку)
```
