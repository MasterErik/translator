// Command f123_calib — калибровка F12.3 (supersede in-flight) по sessLog.
//
// Считает по session_*.csv, как часто фрагмент/уточнение приходил в окно,
// когда первое задание уже вышло из дебаунса и генерация была активна
// (целевой путь F12.3: старое поведение = второй LLM-вызов на тот же вопрос).
//
// Маркеры (строки DEBUG dispatcher'а):
//
//	дописан ... задание перегенерируется: full=  — F12.3-кандидат (второе задание ставится)
//	дописан ... под дебаунсом: full=             — F12.2-путь (не F12.3)
//	дописан ... (F1 ещё не нажат): full=         — QIdle, генерации нет
//	прикреплён уточнением метки                  — QReady/QFailed (поздний, не F12.3)
//	запросов на генерирование: question=         — старт генерации
//	подсказки сгенерированы: question=           — завершение генерации
//	генерация отменена                           — отмена (Esc/shutdown)
//	вопрос пропущен                              — дроп задания
//
// Для каждого F12.3-кандидата выводится контекст: время от старта генерации
// до прихода фрагмента (окно возможности) и время от фрагмента до второго
// старта (ожидание в FIFO).
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	mRegen    = "задание перегенерируется: full="
	mPending  = "под дебаунсом: full="
	mIdleFrag = "(F1 ещё не нажат): full="
	mLate     = "прикреплён уточнением метки"
	mGenStart = "запросов на генерирование: question="
	mGenDone  = "подсказки сгенерированы: question="
	mCancel   = "генерация отменена"
	mDrop     = "вопрос пропущен"
)

type event struct {
	ts       time.Time
	kind     string // regen|pending|idle|late|genstart|gendone|cancel|drop
	question string // full= / question= payload
}

func main() {
	dir := flag.String("dir", "logs", "каталог с session_*.csv")
	templates := flag.Bool("templates", false, "гистограмма шаблонов DEBUG-строк (инвентарь маркеров старых сессий)")
	templatesMax := flag.Int("templates-max", 48, "длина шаблона")
	pairwise := flag.Bool("pairwise", false, "аналоговая калибровка: пары похожих вопросов подряд (Jaccard ≥ 0.75)")
	flag.Parse()

	entries, err := os.ReadDir(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", *dir, err)
		os.Exit(1)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "session_") && strings.HasSuffix(e.Name(), ".csv") {
			files = append(files, filepath.Join(*dir, e.Name()))
		}
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "нет session_*.csv")
		os.Exit(1)
	}

	if *templates {
		modeTemplates(files, *templatesMax)
		return
	}
	if *pairwise {
		modePairwise(files)
		return
	}

	var totals map[string]int
	var regenCases []string
	for _, f := range files {
		events, err := scanFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", filepath.Base(f), err)
			continue
		}
		summary, cases := analyze(filepath.Base(f), events)
		if summary == "" {
			continue
		}
		fmt.Println(summary)
		regenCases = append(regenCases, cases...)
		if totals == nil {
			totals = map[string]int{}
		}
		for _, ev := range events {
			totals[ev.kind]++
		}
	}

	fmt.Println("\n=== ИТОГО по всем сессиям ===")
	kinds := []string{"genstart", "gendone", "cancel", "drop", "regen", "pending", "idle", "late"}
	for _, k := range kinds {
		fmt.Printf("  %-9s %d\n", k, totals[k])
	}
	if len(regenCases) == 0 {
		fmt.Println("\nF12.3-случаев НЕ зафиксировано: задача «не воспроизводится».")
		return
	}
	fmt.Printf("\n=== F12.3-кандидаты (%d шт; каждый = второй LLM-вызов) ===\n", len(regenCases))
	for _, c := range regenCases {
		fmt.Println(c)
	}
}

// modeTemplates печатает гистограмму шаблонов DEBUG-сообщений: числовые
// и quoted-значения затираются, чтобы старые и новые маркеры различались.
func modeTemplates(files []string, maxLen int) {
	counts := map[string]int{}
	samples := map[string]string{}
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		rows, err := csv.NewReader(fh).ReadAll()
		_ = fh.Close()
		if err != nil {
			continue
		}
		for _, row := range rows {
			if len(row) < 4 || row[2] != "DEBUG" {
				continue
			}
			tpl := templateOf(row[3], maxLen)
			counts[tpl]++
			if _, ok := samples[tpl]; !ok {
				samples[tpl] = row[3]
			}
		}
	}
	type kv struct {
		k string
		n int
	}
	var list []kv
	for k, n := range counts {
		list = append(list, kv{k, n})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].n > list[j].n })
	for _, it := range list {
		if strings.Contains(it.k, "dispatcher") {
			fmt.Printf("%-6d %s\n         sample: %q\n", it.n, trunc(it.k, maxLen), trunc(samples[it.k], 110))
		}
	}
}

// templateOf обрезает сообщение до 60 символов дальше последнего "= " и
// затирает цифры и содержимое после последнего "=" — контентная часть
// не важна, важен только шаблон маркера.
func templateOf(msg string, maxLen int) string {
	t := truncateDigits(msg)
	if len(t) > maxLen {
		t = t[:maxLen]
	}
	return t
}

// truncateDigits заменяет последовательности цифр на <N> — параметризованные
// части сообщений (idx, счётчики) не порождают лишние шаблоны.
func truncateDigits(s string) string {
	var b strings.Builder
	wasDigit := false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			if !wasDigit {
				b.WriteString("<N>")
				wasDigit = true
			}
			continue
		}
		wasDigit = false
		b.WriteRune(r)
	}
	return b.String()
}

// modePairwise — аналог калибровки на СТАРЫХ сессиях (без F4/F12): для
// каждой пары подряд идущих «обнаружен вопрос» выясняет, детектится ли
// она дедупом F4 (Jaccard ≥ 0.75 после нормализации) и попадал ли второй
// вопрос в F12.3-окно (Δ > 2s дебаунса → задание уже выставлено).
func modePairwise(files []string) {
	const (
		dupThreshold = 0.75
		debounce     = 2 * time.Second
	)
	totalPairs, dupPairs := 0, 0
	var inWindow, outWindow []string

	for _, f := range files {
		type qev struct {
			ts time.Time
			q  string
		}
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		rows, err := csv.NewReader(fh).ReadAll()
		_ = fh.Close()
		if err != nil {
			continue
		}
		var qs []qev
		for _, row := range rows {
			if len(row) < 4 || row[2] != "DEBUG" {
				continue
			}
			msg := row[3]
			// Приём нового вопроса: старые и новые сессии — один маркер
			// handleQuestion на новом меточном пути.
			if !strings.Contains(msg, "запуск генерации: text=") {
				continue
			}
			ts, err := parseTs(row[0])
			if err != nil {
				continue
			}
			qs = append(qs, qev{ts, after(msg, "запуск генерации: text=")})
		}
		for i := 1; i < len(qs); i++ {
			a, b := qs[i-1].q, qs[i].q
			j := tokenJaccardLocal(questionTokensLocal(a), questionTokensLocal(b))
			if j < dupThreshold {
				continue
			}
			totalPairs++
			dupPairs++
			delta := qs[i].ts.Sub(qs[i-1].ts)
			line := fmt.Sprintf("%s | Δ=%-6.1fs J=%.2f | Q1=%q → Q2=%q",
				filepath.Base(f), delta.Seconds(), j, trunc(a, 46), trunc(b, 46))
			if delta > debounce {
				inWindow = append(inWindow, line)
			} else {
				outWindow = append(outWindow, line)
			}
		}
	}

	fmt.Println("=== Аналоговая калибровка (пары похожих вопросов подряд, J≥0.75) ===")
	fmt.Println("Δ ≤ 2s  → F12.2 (debounce) ловит: задание ещё под дебаунсом, refined заменяет — 1 вызов.")
	fmt.Println("Δ > 2s  → F12.3-окно: задание вышло из дебаунса (генерация активна) — 2 вызова без supersede.")
	fmt.Printf("\nВсего пар-дублей: %d\n  Δ ≤ 2s (ловит F12.2):  %d\n  Δ > 2s (окно F12.3):   %d\n\n",
		totalPairs, len(outWindow), len(inWindow))
	fmt.Println("--- Δ ≤ 2s (F12.2) ---")
	for _, l := range outWindow {
		fmt.Println(l)
	}
	fmt.Println("--- Δ > 2s (F12.3) ---")
	for _, l := range inWindow {
		fmt.Println(l)
	}
}

// questionTokensLocal/tokenJaccardLocal — копия метрики дедупа F4
// (dispatcher/dedup.go) без импорта internal-пакета: анализатор ходит
// по логам, а не по коду.
var dedupStopTokens = map[string]struct{}{
	"so": {}, "ok": {}, "okay": {}, "right": {}, "yeah": {}, "well": {},
}

func questionTokensLocal(text string) []string {
	sanitized := mapRunes(text)
	tokens := strings.Fields(sanitized)
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if _, stop := dedupStopTokens[t]; stop {
			continue
		}
		out = append(out, t)
	}
	return out
}

func mapRunes(text string) string {
	var b strings.Builder
	for _, r := range text {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '\'', r >= 'а' && r <= 'я', r >= 'А' && r <= 'Я':
			b.WriteRune(lowerRune(r))
		default:
			b.WriteRune(' ')
		}
	}
	return b.String()
}

func lowerRune(r rune) rune {
	if r >= 'А' && r <= 'Я' {
		return r + 32
	}
	return r
}

// tokenJaccardLocal — Жаккар по множествам токенов (повторы игнорируются).
func tokenJaccardLocal(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	ma := make(map[string]struct{}, len(a))
	for _, t := range a {
		ma[t] = struct{}{}
	}
	mb := make(map[string]struct{}, len(b))
	for _, t := range b {
		mb[t] = struct{}{}
	}
	inter := 0
	for t := range ma {
		if _, in := mb[t]; in {
			inter++
		}
	}
	return float64(inter) / float64(len(ma)+len(mb)-inter)
}

// scanFile читает CSV и классифицирует DEBUG-строки dispatcher'а.
func scanFile(path string) ([]event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	var out []event
	for _, row := range rows {
		if len(row) < 4 || row[2] != "DEBUG" {
			continue
		}
		ts, err := parseTs(row[0])
		if err != nil {
			continue
		}
		msg := row[3]
		kind, payload := classify(msg)
		if kind == "" {
			continue
		}
		out = append(out, event{ts: ts, kind: kind, question: payload})
	}
	return out, nil
}

func classify(msg string) (kind, payload string) {
	switch {
	case strings.Contains(msg, mRegen):
		return "regen", after(msg, mRegen)
	case strings.Contains(msg, mPending):
		return "pending", after(msg, mPending)
	case strings.Contains(msg, mIdleFrag):
		return "idle", after(msg, mIdleFrag)
	case strings.Contains(msg, mLate):
		return "late", ""
	case strings.Contains(msg, mGenStart):
		return "genstart", questionOf(msg)
	case strings.Contains(msg, mGenDone):
		return "gendone", questionOf(msg)
	case strings.Contains(msg, mCancel):
		return "cancel", ""
	case strings.Contains(msg, mDrop):
		return "drop", ""
	}
	return "", ""
}

func after(msg, marker string) string {
	i := strings.Index(msg, marker)
	if i < 0 {
		return ""
	}
	return msg[i+len(marker):]
}

// parseTs парсит время sessLog: без таймзоны (RFC3339Nano взялся бы
// ошибкой — файл пишется с "2006-01-02T15:04:05.000").
func parseTs(s string) (time.Time, error) {
	for _, layout := range []string{
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		time.RFC3339Nano,
	} {
		if ts, err := time.Parse(layout, s); err == nil {
			return ts, nil
		}
	}
	return time.Time{}, fmt.Errorf("time parse: %q", s)
}

// questionOf вытаскивает question=... (до ", command=") из genstart/gendone.
func questionOf(msg string) string {
	i := strings.Index(msg, "question=")
	if i < 0 {
		return ""
	}
	rest := msg[i+len("question="):]
	if j := strings.Index(rest, ", command="); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// analyze считает маркеры одной сессии и сопоставляет regen-кейсы
// с генерациями: окно возможности (regen - пред. genstart) и ожидание
// в FIFO (след. genstart с полным текстом - regen).
func analyze(name string, events []event) (string, []string) {
	if len(events) == 0 {
		return "", nil
	}
	counts := map[string]int{}
	for _, ev := range events {
		counts[ev.kind]++
	}
	summary := fmt.Sprintf("%-34s genstart=%-3d gendone=%-3d regen(F12.3)=%d pending(F12.2)=%d idle=%d late=%d cancel=%d drop=%d",
		name, counts["genstart"], counts["gendone"], counts["regen"], counts["pending"], counts["idle"], counts["late"], counts["cancel"], counts["drop"])

	var cases []string
	for i, ev := range events {
		if ev.kind != "regen" {
			continue
		}
		prevStart, prevQ := time.Time{}, ""
		for j := i - 1; j >= 0; j-- {
			if events[j].kind == "genstart" {
				prevStart, prevQ = events[j].ts, events[j].question
				break
			}
		}
		nextStart := time.Time{}
		for j := i + 1; j < len(events); j++ {
			if events[j].kind == "genstart" &&
				(strings.HasPrefix(events[j].question, ev.question) || strings.HasPrefix(ev.question, events[j].question)) {
				nextStart = events[j].ts
				break
			}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "  %s | фрагмент пришёл через %s после старта генерации (prev Q: %q)",
			ev.ts.Format("01-02 15:04:05"), fmtGap(ev.ts.Sub(prevStart)), trunc(prevQ, 50))
		if !nextStart.IsZero() {
			fmt.Fprintf(&b, "; перегенерация стартовала через %s, Q: %q", fmtGap(nextStart.Sub(ev.ts)), trunc(ev.question, 60))
		} else {
			b.WriteString("; второй старт генерации НЕ найден (дроп/отмена/заглушено?)")
		}
		cases = append(cases, b.String())
	}
	return summary, cases
}

func fmtGap(d time.Duration) string {
	if d <= 0 {
		return "~"
	}
	return d.Truncate(time.Millisecond).String()
}

func trunc(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// сортировка не нужна: CSV уже хронологичен; оставлено для ясности.
var _ = sort.Strings
