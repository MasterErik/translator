// Command context_eval evaluates fact-level retrieval quality of the candidate
// context against JSON evaluation datasets.
//
// Прогон идёт через ТОТ ЖЕ вход, что у прода (candidatecontext.BuildContextFn):
// метрики считаются по итоговому контексту — фактам, подсказкам и их порядку
// в промпте, а не по сырому ranking'у. Печатаются Recall@1/3/5, MRR, LeakRate
// (must_not в выдаче), EmptyRate (корректные пустые выдачи) и FalseShowRate
// (показанный готовый ответ не из relevant), плюс срезы по тегам.
//
// Разметка валидируется до прогона: неизвестный FactID, split не из этого
// файла, пустой relevant без тега no_relevant, дубль id — ошибка и ненулевой
// код возврата (метрики по битому набору бессмысленны).
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	candidatecontext "github.com/mastererik/translator/internal/context"
)

// datasetSpec — один набор в прогоне.
type datasetSpec struct {
	name  string // имя в таблице
	path  string
	v2    bool   // v2-набор: полная проверка полей и сверка split с файлом
	split string // ожидаемый split ("train"/"holdout"; пусто — без сверки)
}

func main() {
	dir := flag.String("dir", "./candidate_context", "path to candidate_context directory (manifest.json + sections/)")
	hintsDir := flag.String("hints-dir", "hints", "path to hints directory (пустая строка отключает подсказки)")
	train := flag.String("train", "", "path to legacy train dataset (JSON)")
	holdout := flag.String("holdout", "", "path to legacy holdout dataset (JSON)")
	v2 := flag.String("v2", "", "v2-наборы через запятую в виде split=path (например train=internal/context/testdata/eval_v2_train.json)")
	slices := flag.Bool("slices", false, "печатать срезы по тегам для v2-наборов")
	validateOnly := flag.Bool("validate-only", false, "только валидация разметки, без прогона retrieval")
	topK := flag.Int("top", 5, "retrieval top-K")
	minScore := flag.Float64("min-score", 0, "min retrieval score")
	maxTokens := flag.Int("max-tokens", candidatecontext.DefaultMaxTokens, "бюджет контекста в токенах (0 = дефолт пакета; как в config.yaml)")
	maxProfileTokens := flag.Int("max-profile-tokens", candidatecontext.DefaultMaxProfileTokens, "бюджет профиля в токенах (0 = дефолт пакета)")
	maxMatrixTokens := flag.Int("max-matrix-tokens", candidatecontext.DefaultMaxMatrixTokens, "бюджет technical_matrix в токенах (0 = дефолт пакета)")
	hintsTopK := flag.Int("hints-top", 1, "top-K подсказок")
	hintsMinScore := flag.Float64("hints-min-score", 50, "min score подсказок (как в config.yaml)")
	onLoadError := flag.String("on-load-error", candidatecontext.OnLoadErrorFail, "поведение при сбое загрузки каталога: fail|degraded")
	flag.Parse()

	knownIDs, dupes, err := collectFactIDs(*dir, *hintsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(dupes) > 0 {
		fmt.Fprintf(os.Stderr, "error: FactID встречается и в candidate_context, и в hints: %s\n", strings.Join(dupes, ", "))
		os.Exit(1)
	}

	specs := buildSpecs(*train, *holdout, *v2)

	// Валидация разметки — до сборки retriever'а: битый набор должен падать
	// независимо от состояния каталогов.
	fmt.Printf("%-14s %-7s %7s %7s %8s\n", "dataset", "split", "cases", "status", "проблемы")
	var failed bool
	type loadedDataset struct {
		spec datasetSpec
		ds   *candidatecontext.EvalDataset
	}
	var loaded []loadedDataset
	for _, spec := range specs {
		ds, err := candidatecontext.LoadEvalDataset(spec.path)
		if err != nil {
			fmt.Printf("%-14s %-7s %7s %7s %v\n", spec.name, spec.split, "-", "ERROR", err)
			failed = true
			continue
		}
		errs := candidatecontext.ValidateEvalDataset(ds, candidatecontext.ValidateOptions{
			Split:        spec.split,
			KnownFactIDs: knownIDs,
			RequireV2:    spec.v2,
		})
		status := "OK"
		if len(errs) > 0 {
			status = "INVALID"
			failed = true
		}
		fmt.Printf("%-14s %-7s %7d %7s %d\n", spec.name, spec.split, len(ds.Queries), status, len(errs))
		for _, e := range errs {
			fmt.Printf("    - %v\n", e)
		}
		loaded = append(loaded, loadedDataset{spec: spec, ds: ds})
	}

	if failed {
		fmt.Fprintln(os.Stderr, "error: разметка невалидна, прогон метрик не имеет смысла")
		os.Exit(1)
	}
	if *validateOnly {
		fmt.Println("validate-only: разметка валидна")
		return
	}

	cfg := candidatecontext.ContextFnConfig{
		Dir:              *dir,
		TopK:             *topK,
		MinScore:         *minScore,
		MaxTokens:        *maxTokens,
		MaxProfileTokens: *maxProfileTokens,
		MaxMatrixTokens:  *maxMatrixTokens,
		HintsTopK:        *hintsTopK,
		HintsMinScore:    *hintsMinScore,
		OnLoadError:      *onLoadError,
		Logf:             func(msg string) { fmt.Fprintf(os.Stderr, "%s\n", msg) },
	}
	if *hintsDir != "" {
		cfg.HintsDir = *hintsDir
	}
	fn, err := candidatecontext.BuildContextFn(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n%-14s %-13s %7s %7s %7s %7s %7s %7s %7s %8s %10s\n",
		"dataset", "split", "cases", "scored", "R@1", "R@3", "R@5", "MRR", "Leak", "Empty", "FalseShow")
	for _, item := range loaded {
		report := candidatecontext.EvaluateContextFn(fn, item.ds)
		printMetrics(item.spec.name, item.spec.split, report.Overall)
		if item.spec.v2 {
			// Gate — по нему принимается решение «recall не просел»: без known_gap.
			printMetrics(item.spec.name, item.spec.split+"/gate", report.Gate)
			if *slices {
				for _, key := range report.SortedSliceKeys() {
					printMetrics(item.spec.name, item.spec.split+"/"+key, report.Slices[key])
				}
			}
		}
	}
}

// printMetrics печатает одну строку таблицы метрик.
func printMetrics(name, split string, m candidatecontext.EvalMetrics) {
	fmt.Printf("%-14s %-13s %7d %7d %7.3f %7.3f %7.3f %7.3f %8.3f %8.3f %10.3f\n",
		name, split, m.Queries, m.ScoredQueries,
		m.RecallAt1, m.RecallAt3, m.RecallAt5, m.MRR,
		m.LeakRate, m.EmptyRate, m.FalseShowRate)
}

// collectFactIDs собирает FactID обоих каталогов и возвращает пересечение ID
// (непустое пересечение делает разметку неоднозначной).
func collectFactIDs(dir, hintsDir string) (map[string]bool, []string, error) {
	ccIDs, err := candidatecontext.LoadFactIDs(dir)
	if err != nil {
		return nil, nil, err
	}
	if hintsDir == "" {
		return ccIDs, nil, nil
	}
	hintsIDs, err := candidatecontext.LoadFactIDs(hintsDir)
	if err != nil {
		return nil, nil, err
	}
	merged, dupes := candidatecontext.MergeFactIDSets(ccIDs, hintsIDs)
	return merged, dupes, nil
}

// buildSpecs собирает список наборов из флагов: legacy train/holdout и v2-пары
// "split=path".
func buildSpecs(train, holdout, v2List string) []datasetSpec {
	var specs []datasetSpec
	if train != "" {
		specs = append(specs, datasetSpec{name: "legacy-train", path: train, split: "train"})
	}
	if holdout != "" {
		specs = append(specs, datasetSpec{name: "legacy-hold", path: holdout, split: "holdout"})
	}
	for _, item := range strings.Split(v2List, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		split, path, ok := strings.Cut(item, "=")
		if !ok {
			fmt.Fprintf(os.Stderr, "error: -v2 ожидает split=path, получено %q\n", item)
			os.Exit(2)
		}
		specs = append(specs, datasetSpec{name: "v2-" + split, path: path, v2: true, split: split})
	}
	return specs
}
