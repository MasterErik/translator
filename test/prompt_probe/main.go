// Command prompt_probe — A/B-проверка правил из SYSTEM_PROMPT_FILE на живой модели.
//
// Сравнивает два тела промпта (старое и новое) на общем наборе вопросов через
// те же конструкторы, что прод: common.LoadConfigFromYAML + translator.NewChatProvider.
//
// Запуск:
//
//	go run ./test/prompt_probe -old=path/to/old.md                 # old vs текущий файл
//	go run ./test/prompt_probe -old=old.md -sleep=10 -q="вопрос"   # точечно
//
// Метрики считаются по английской части ответа (до "|"): words, avgWordLen,
// wordsPerSentence, longWords (>10 символов).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/mastererik/translator/internal/common"
	"github.com/mastererik/translator/internal/translator"
)

var defaultQuestions = []string{
	"What is the difference between a mutex and a channel in Go?",
	"How does the JVM garbage collector work, and what is a stop-the-world pause?",
	"Tell us about yourself and your work experience.",
}

const candidateCtx = "Senior Go developer, 5+ years, expert in concurrency patterns. " +
	"Worked with Java 11/17, Spring Boot, Kafka. Led a team of 4 engineers."

type variant struct {
	label string
	body  string
}

type stats struct {
	words            int
	avgWordLen       float64
	wordsPerSentence float64
	longWords        int
}

func main() {
	oldPath := flag.String("old", "", "путь к старому телу промпта (контрольный вариант)")
	dropLine := flag.String("drop", "", "подстрока строки, которую вырезать из текущего промпта (вариант DROP)")
	sleep := flag.Int("sleep", 8, "пауза между запросами, сек (лимит TPM у провайдера)")
	qFlag := flag.String("q", "", "вопросы через | ; по умолчанию набор из трёх")
	flag.Parse()

	cfg, err := common.LoadConfigFromYAML("config.yaml")
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: config.yaml:", err)
		os.Exit(1)
	}
	apiKey := cfg.LLMAPIKey
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "ERROR: no API key")
		os.Exit(1)
	}

	newBody, readErr := os.ReadFile(cfg.SystemPromptFile)
	if readErr != nil {
		fmt.Fprintln(os.Stderr, "ERROR: read new:", readErr)
		os.Exit(1)
	}

	variants := []variant{{"NEW", string(newBody)}}
	if *oldPath != "" {
		oldBody, oldErr := os.ReadFile(*oldPath)
		if oldErr != nil {
			fmt.Fprintln(os.Stderr, "ERROR: read old:", oldErr)
			os.Exit(1)
		}
		variants = []variant{{"OLD", string(oldBody)}, {"NEW", string(newBody)}}
	}
	if *dropLine != "" {
		dropped, ok := dropLines(string(newBody), *dropLine)
		if !ok {
			fmt.Fprintf(os.Stderr, "ERROR: в текущем промпте нет строки с %q\n", *dropLine)
			os.Exit(1)
		}
		variants = append(variants, variant{"DROP", dropped})
	}

	questions := defaultQuestions
	if *qFlag != "" {
		questions = strings.Split(*qFlag, "|")
	}

	fmt.Printf("model=%s new=%s old=%s\n\n", cfg.LLMModel, cfg.SystemPromptFile, *oldPath)
	for _, v := range variants {
		tpl, tplErr := translator.ParseSystemPromptTemplate(v.body)
		if tplErr != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %s parse: %v\n", v.label, tplErr)
			os.Exit(1)
		}
		fmt.Printf("== %s ==\n%s", v.label, tpl.Render(cfg.SourceLang, cfg.TargetLang))
	}

	type agg struct{ words, sentences, longWords, samples int }
	totals := map[string]*agg{}

	for qi, q := range questions {
		fmt.Printf("\n########## QUESTION: %s ##########\n", q)
		for vi, v := range variants {
			if qi > 0 || vi > 0 {
				time.Sleep(time.Duration(*sleep) * time.Second)
			}
			tpl, _ := translator.ParseSystemPromptTemplate(v.body)
			prov := translator.NewChatProvider(cfg.LLMBaseURL, apiKey, cfg.LLMModel)
			prov.SetMaxTokens(cfg.MaxTokens)
			prov.SetSystemPromptTemplate(tpl)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			answers, genErr := prov.GenerateAnswers(ctx, translator.AnswerRequest{
				Question:         strings.TrimSpace(q),
				CandidateContext: candidateCtx,
				SourceLang:       cfg.SourceLang,
				TargetLang:       cfg.TargetLang,
			})
			cancel()
			if genErr != nil {
				fmt.Printf("[%s] ERROR %v\n", v.label, genErr)
				continue
			}
			for _, a := range answers {
				en := englishPart(a)
				st := measure(en)
				sentences := countSentences(en)
				t := totals[v.label]
				if t == nil {
					t = &agg{}
					totals[v.label] = t
				}
				t.words += st.words
				t.sentences += sentences
				t.longWords += st.longWords
				t.samples++
				fmt.Printf("[%s] words=%d avgWordLen=%.2f wordsPerSentence=%.1f longWords=%d\n  %s\n",
					v.label, st.words, st.avgWordLen, st.wordsPerSentence, st.longWords, en)
			}
		}
	}

	fmt.Printf("\n===== SUMMARY =====\n")
	for _, v := range variants {
		t := totals[v.label]
		if t == nil || t.samples == 0 {
			continue
		}
		fmt.Printf("%-4s samples=%d totalWords=%d avgWords=%.1f avgWordsPerSentence=%.1f longWords=%d\n",
			v.label, t.samples, t.words,
			float64(t.words)/float64(t.samples),
			float64(t.words)/float64(t.sentences), t.longWords)
	}
}

// dropLines вырезает из текста строки, содержащие needle (без учёта регистра).
// ok=false — совпадений не было (опечатка в needle не должна молча давать
// «идентичные» варианты, иначе замер бессмысленен).
func dropLines(text, needle string) (string, bool) {
	lower := strings.ToLower(needle)
	var kept []string
	hit := false
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(strings.ToLower(line), lower) {
			hit = true
			continue
		}
		kept = append(kept, line)
	}
	if !hit {
		return "", false
	}
	return strings.Join(kept, "\n"), true
}

// englishPart вырезает английскую часть до "|".
func englishPart(answer string) string {
	answer = strings.TrimSpace(answer)
	if i := strings.Index(answer, "|"); i > 0 {
		answer = answer[:i]
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(answer), "EN:"))
}

func countSentences(s string) int {
	n := 0
	for _, r := range s {
		if r == '.' || r == '!' || r == '?' {
			n++
		}
	}
	if n == 0 {
		n = 1
	}
	return n
}

func measure(s string) stats {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\''
	})
	var st stats
	st.words = len(fields)
	if st.words == 0 {
		return st
	}
	total := 0
	for _, w := range fields {
		n := len([]rune(w))
		total += n
		if n > 10 {
			st.longWords++
		}
	}
	st.avgWordLen = float64(total) / float64(st.words)
	st.wordsPerSentence = float64(st.words) / float64(countSentences(s))
	return st
}
