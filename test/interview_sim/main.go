// Command interview_sim имитирует интервью без транскрибатора: вопросы
// интервьюера подаются напрямую в LLM, промпт собирается так же, как в
// pipeline.New (факты candidate_context по бюджету + hints-подсказка +
// Simple English). Печатает вопрос, сработавшую подсказку (если есть)
// и полный ответ модели в формате "EN: ... | RU: ...".
//
// Использование:
//
//	go run ./test/interview_sim            # список вопросов
//	go run ./test/interview_sim -all        # все вопросы (с паузами против rate limit)
//	go run ./test/interview_sim 3 7        # выборочные вопросы (номера с 1)
//	go run ./test/interview_sim -questions my_questions.txt -all
//
// Формат файла -questions: один вопрос на строку, пустые строки и строки,
// начинающиеся с '#', игнорируются. Номера вопросов в выводе — 1-based.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mastererik/translator/internal/common"
	candidatecontext "github.com/mastererik/translator/internal/context"
	"github.com/mastererik/translator/internal/translator"
)

// defaultQuestions — вопросы в манере рекрутера/интервьюера (English).
// Редактируйте список под конкретную подготовку к интервью.
var defaultQuestions = []string{
	"Could you describe your most recent project?",
	"What is your approach to SQL versus ORM?",
	"Do you have experience with PostgreSQL?",
	"What is your educational background?",
	"How do you handle deduplication of files from external sources?",
	"Tell me about your experience with Kafka.",
	"What happens with orphaned files in S3?",
	"Have you worked with Redis?",
	"Describe your experience with high-load systems.",
	"What monitoring and metrics do you use in production?",
	"Have you ever worked with Kubernetes or Terraform?",
	"Why did you choose HashMap instead of Kafka Streams?",
}

const usage = `interview_sim — проверка качества ответов LLM без транскрибатора.

Использование:
  go run ./test/interview_sim            показать список вопросов
  go run ./test/interview_sim -all       прогнать все вопросы
  go run ./test/interview_sim 3 7        прогнать выборочные вопросы (номера с 1)
  go run ./test/interview_sim -questions file.txt -all

Флаги:`

func main() {
	all := flag.Bool("all", false, "run all questions (with pause between LLM calls)")
	questionsFile := flag.String("questions", "", "file with questions: one per line, '#' comments")
	pause := flag.Int("pause", 35, "seconds between LLM calls in -all mode (Groq TPM rate limit)")
	timeout := flag.Int("timeout", 90, "per-question LLM timeout, seconds")
	flag.Usage = func() { fmt.Fprintln(os.Stderr, usage); flag.PrintDefaults() }
	flag.Parse()

	questions := defaultQuestions
	if *questionsFile != "" {
		qs, err := loadQuestions(*questionsFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		questions = qs
	}

	// Выбор вопросов: позиционные аргументы — номера (1-based).
	selected := map[int]bool{}
	for _, a := range flag.Args() {
		n, err := strconv.Atoi(a)
		if err != nil || n < 1 || n > len(questions) {
			fmt.Fprintf(os.Stderr, "error: неверный номер вопроса %q (допустимо 1..%d)\n", a, len(questions))
			os.Exit(1)
		}
		selected[n] = true
	}

	// Без -all и без номеров — только показать вопросы.
	if !*all && len(selected) == 0 {
		fmt.Println("Доступные вопросы (запуск: -all или номера):")
		for i, q := range questions {
			fmt.Printf("%2d. %s\n", i+1, q)
		}
		return
	}

	cfg, err := common.LoadConfigFromYAML("config.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Fact-level candidate context (как resolveCandidateContext в pipeline).
	manifest, index, err := candidatecontext.LoadCandidateContext(cfg.CandidateContext.Dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: candidate context: %v\n", err)
		os.Exit(1)
	}
	retriever, err := candidatecontext.NewRetriever(manifest, index, cfg.CandidateContext.Dir, cfg.CandidateContext.TopK, cfg.CandidateContext.MinScore)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: candidate context retriever: %v\n", err)
		os.Exit(1)
	}
	budgeter := candidatecontext.NewBudgeter(cfg.CandidateContext.MaxTokens, cfg.CandidateContext.MaxProfileTokens)

	// Hints (как resolveHints в pipeline; ошибки не фатальны).
	var hintsFn func(string) string
	if cfg.Hints.Dir != "" {
		hMan, hIdx, hErr := candidatecontext.LoadCandidateContext(cfg.Hints.Dir)
		if hErr != nil {
			fmt.Fprintf(os.Stderr, "WARN: hints: %v\n", hErr)
		} else {
			hR, rErr := candidatecontext.NewRetriever(hMan, hIdx, cfg.Hints.Dir, cfg.Hints.TopK, cfg.Hints.MinScore)
			if rErr != nil {
				fmt.Fprintf(os.Stderr, "WARN: hints retriever: %v\n", rErr)
			} else {
				hintsFn = func(q string) string {
					var sb strings.Builder
					for i, res := range hR.Retrieve(q) {
						if i > 0 {
							sb.WriteString("\n")
						}
						sb.WriteString(res.Content)
					}
					return sb.String()
				}
			}
		}
	}

	provider := translator.NewChatProvider(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)

	first := true
	failures := 0
	executed := 0
	for i, q := range questions {
		n := i + 1
		if !*all && !selected[n] {
			continue
		}
		if !first && *all {
			fmt.Printf("--- пауза %d c ---\n", *pause)
			time.Sleep(time.Duration(*pause) * time.Second)
		}
		first = false
		executed++

		// Промпт: факты по бюджету + подсказка (как appendHint в pipeline).
		cc := budgeter.Budget(manifest.Profile, retriever.Retrieve(q)).Render()
		hint := ""
		if hintsFn != nil {
			hint = hintsFn(q)
			if hint != "" {
				cc += "\n\nSuggested answer (use as is):\n" + hint
			}
		}

		fmt.Printf("\n================ Q%d ================\n", n)
		fmt.Printf("QUESTION: %s\n", q)
		if hint == "" {
			fmt.Println("HINT: (none)")
		} else {
			fmt.Printf("HINT: %s\n", strings.ReplaceAll(hint, "\n", " "))
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Second)
		answers, err := provider.GenerateAnswers(ctx, translator.AnswerRequest{
			Question:         q,
			CandidateContext: cc,
			Command:          translator.CommandSimplerEnglish,
			SourceLang:       cfg.SourceLang,
			TargetLang:       cfg.TargetLang,
		})
		cancel()
		if err != nil {
			fmt.Printf("ERROR: %v\n", err)
			failures++
			continue
		}
		for _, a := range answers {
			fmt.Printf("ANSWER: %s\n", a)
		}
	}

	fmt.Printf("\n=== готово: %d вопрос(ов), ошибок: %d ===\n", executed, failures)
}

// loadQuestions читает вопросы из файла: одна строка — один вопрос;
// пустые строки и комментарии '#' пропускаются.
func loadQuestions(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("interview_sim: чтение файла вопросов: %w", err)
	}
	defer f.Close()

	var qs []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		qs = append(qs, line)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("interview_sim: разбор файла вопросов: %w", err)
	}
	if len(qs) == 0 {
		return nil, fmt.Errorf("interview_sim: файл вопросов %s пуст", path)
	}
	return qs, nil
}
