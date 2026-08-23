package candidatecontext

import (
	"reflect"
	"sync"
	"testing"
)

// --- NewTokenizer ---

func TestNewTokenizerValid(t *testing.T) {
	aliases := map[string][]string{
		"kubernetes": {"k8s", "kube"},
		"postgresql": {"postgres", "pg"},
	}
	tok, err := NewTokenizer(aliases)
	if err != nil {
		t.Fatalf("NewTokenizer: неожиданная ошибка: %v", err)
	}
	if tok == nil {
		t.Fatal("NewTokenizer: вернул nil tokenizer")
	}
	if got := tok.Canonicalize([]string{"k8s", "pg", "unknown"}); !reflect.DeepEqual(got, []string{"kubernetes", "postgresql", "unknown"}) {
		t.Errorf("Canonicalize: got %v", got)
	}
}

func TestNewTokenizerEmptyAndNil(t *testing.T) {
	for name, aliases := range map[string]map[string][]string{
		"nil":   nil,
		"empty": {},
	} {
		t.Run(name, func(t *testing.T) {
			tok, err := NewTokenizer(aliases)
			if err != nil {
				t.Fatalf("NewTokenizer(%s): неожиданная ошибка: %v", name, err)
			}
			if tok == nil {
				t.Fatalf("NewTokenizer(%s): вернул nil", name)
			}
			if got := tok.Process("hello world"); !reflect.DeepEqual(got, []string{"hello", "world"}) {
				t.Errorf("Process: got %v", got)
			}
		})
	}
}

func TestNewTokenizerAliasConflict(t *testing.T) {
	aliases := map[string][]string{
		"kubernetes": {"k8s"},
		"kafka":      {"k8s"}, // k8s уже заявлен kubernetes.
	}
	if _, err := NewTokenizer(aliases); err == nil {
		t.Fatal("NewTokenizer: ожидалась ошибка конфликта alias")
	}
}

func TestNewTokenizerCycle(t *testing.T) {
	aliases := map[string][]string{
		"kubernetes": {"k8s"},
		"k8s":        {"kube"}, // canonical k8s является вариантом kubernetes.
	}
	if _, err := NewTokenizer(aliases); err == nil {
		t.Fatal("NewTokenizer: ожидалась ошибка цикла canonicalization")
	}
}

func TestNewTokenizerEmptyCanonical(t *testing.T) {
	aliases := map[string][]string{
		"": {"k8s"},
	}
	if _, err := NewTokenizer(aliases); err == nil {
		t.Fatal("NewTokenizer: ожидалась ошибка пустого canonical term")
	}
}

func TestNewTokenizerEmptyAlias(t *testing.T) {
	aliases := map[string][]string{
		"kubernetes": {"k8s", ""},
	}
	if _, err := NewTokenizer(aliases); err == nil {
		t.Fatal("NewTokenizer: ожидалась ошибка пустого варианта")
	}
}

func TestNewTokenizerDuplicateAliasSameCanonical(t *testing.T) {
	// Дублирование одного alias в списке одного canonical — не ошибка.
	aliases := map[string][]string{
		"kubernetes": {"k8s", "k8s"},
	}
	tok, err := NewTokenizer(aliases)
	if err != nil {
		t.Fatalf("NewTokenizer: неожиданная ошибка: %v", err)
	}
	if got := tok.Canonicalize([]string{"k8s"}); !reflect.DeepEqual(got, []string{"kubernetes"}) {
		t.Errorf("Canonicalize: got %v", got)
	}
}

func TestNewTokenizerSelfReferenceAllowed(t *testing.T) {
	// Самоссылка (canonical в собственном списке) — не цикл, no-op.
	aliases := map[string][]string{
		"kubernetes": {"k8s", "kubernetes"},
	}
	tok, err := NewTokenizer(aliases)
	if err != nil {
		t.Fatalf("NewTokenizer: неожиданная ошибка: %v", err)
	}
	if got := tok.Canonicalize([]string{"kubernetes", "k8s"}); !reflect.DeepEqual(got, []string{"kubernetes", "kubernetes"}) {
		t.Errorf("Canonicalize: got %v", got)
	}
}

// --- Normalize ---

func TestNormalize(t *testing.T) {
	tok, err := NewTokenizer(nil)
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"lowercase", "Hello WORLD", "hello world"},
		{"trim space", "  k8s  ", "k8s"},
		{"unicode", "КУБЕРНЕТЕС", "кубернетес"},
		{"empty", "", ""},
		{"only spaces", "   ", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tok.Normalize(c.in); got != c.want {
				t.Errorf("Normalize(%q): got %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// --- Tokenize ---

func TestTokenize(t *testing.T) {
	tok, err := NewTokenizer(nil)
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"split fields", "a b c", []string{"a", "b", "c"}},
		{"trim punctuation", "hello, world!", []string{"hello", "world"}},
		{"k8s preserved", "k8s cluster", []string{"k8s", "cluster"}},
		{"internal hyphen", "low-latency", []string{"low-latency"}},
		{"trailing comma", "postgresql,", []string{"postgresql"}},
		{"leading punct", "(k8s)", []string{"k8s"}},
		{"punct only dropped", "!!! ,,,", []string{}},
		{"empty", "", []string{}},
		{"digits kept", "42 answers", []string{"42", "answers"}},
		{"unicode letters", "кубернетес, k8s", []string{"кубернетес", "k8s"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := tok.Tokenize(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Tokenize(%q): got %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// --- RemoveStopWords ---

func TestRemoveStopWords(t *testing.T) {
	tok, err := NewTokenizer(nil)
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			"english",
			[]string{"the", "and", "deploy", "kubernetes", "is", "fast"},
			[]string{"deploy", "kubernetes", "fast"},
		},
		{
			"russian",
			[]string{"я", "работал", "с", "k8s", "и", "это"},
			[]string{"работал", "k8s"},
		},
		{
			"order preserved",
			[]string{"a", "b", "c", "the"},
			[]string{"b", "c"},
		},
		{
			"no stop words",
			[]string{"kubernetes", "postgresql"},
			[]string{"kubernetes", "postgresql"},
		},
		{
			"all stop words",
			[]string{"the", "и", "не", "is"},
			[]string{},
		},
		{
			"empty",
			[]string{},
			[]string{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := tok.RemoveStopWords(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("RemoveStopWords(%v): got %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// --- Canonicalize ---

func TestCanonicalize(t *testing.T) {
	tok, err := NewTokenizer(map[string][]string{
		"kubernetes": {"k8s", "kube"},
		"postgresql": {"postgres", "pg"},
	})
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			"alias replaced",
			[]string{"k8s", "deploy"},
			[]string{"kubernetes", "deploy"},
		},
		{
			"non-alias stays",
			[]string{"java", "kafka"},
			[]string{"java", "kafka"},
		},
		{
			"order preserved",
			[]string{"pg", "k8s", "x", "kube"},
			[]string{"postgresql", "kubernetes", "x", "kubernetes"},
		},
		{
			"empty",
			[]string{},
			[]string{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := tok.Canonicalize(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Canonicalize(%v): got %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// --- Process ---

func TestProcess(t *testing.T) {
	tok, err := NewTokenizer(map[string][]string{
		"kubernetes": {"k8s", "kube"},
		"postgresql": {"postgres", "pg"},
	})
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			"full pipeline en",
			"K8s and PostgreSQL, cluster.",
			[]string{"kubernetes", "postgresql", "cluster"},
		},
		{
			"full pipeline ru",
			"Я работал с k8s и это pg",
			[]string{"работал", "kubernetes", "postgresql"},
		},
		{
			"empty string",
			"",
			[]string{},
		},
		{
			"only stop words",
			"the and is a",
			[]string{},
		},
		{
			"no aliases",
			"deploy java service",
			[]string{"deploy", "java", "service"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := tok.Process(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Process(%q): got %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// --- Иммутабельность ---

func TestTokenizerImmutability(t *testing.T) {
	tok, err := NewTokenizer(map[string][]string{
		"kubernetes": {"k8s", "kube"},
	})
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	input := []string{"k8s", "and", "kube", "deploy"}

	// Повторный вызов даёт одинаковый результат.
	first := tok.Process("k8s and kube deploy")
	second := tok.Process("k8s and kube deploy")
	if !reflect.DeepEqual(first, second) {
		t.Errorf("Process: повторный вызов дал разный результат: %v vs %v", first, second)
	}

	// Canonicalize не мутирует входной слайс.
	original := append([]string(nil), input...)
	got := tok.Canonicalize(input)
	if !reflect.DeepEqual(input, original) {
		t.Errorf("Canonicalize мутировал входной слайс: %v -> %v", original, input)
	}
	want := []string{"kubernetes", "and", "kubernetes", "deploy"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Canonicalize: got %v, want %v", got, want)
	}
}

// --- Конкурентность ---

// TestTokenizerConcurrent проверяет потокобезопасность Tokenizer: 100 горутин
// параллельно вызывают Tokenize, Canonicalize, RemoveStopWords и Process над
// общим экземпляром. Результаты сравниваются с эталонами, посчитанными
// однопоточно тем же Tokenizer до запуска горутин. Запуск с -race не должен
// показывать WARNING: DATA RACE.
func TestTokenizerConcurrent(t *testing.T) {
	tok, err := NewTokenizer(map[string][]string{
		"kubernetes": {"k8s", "kube"},
		"postgresql": {"postgres", "pg"},
	})
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}

	tokenizeInputs := []string{
		"K8s and PostgreSQL, cluster.",
		"low-latency deploy",
		"!!! ,,,",
		"кубернетес, k8s",
	}
	canonicalInputs := [][]string{
		{"k8s", "deploy"},
		{"java", "kafka"},
		{"pg", "k8s", "x", "kube"},
		{},
	}
	stopInputs := [][]string{
		{"the", "and", "deploy", "kubernetes", "is", "fast"},
		{"я", "работал", "с", "k8s", "и", "это"},
		{"a", "b", "c", "the"},
		{"kubernetes", "postgresql"},
		{"the", "и", "не", "is"},
		{},
	}
	processInputs := []string{
		"K8s and PostgreSQL, cluster.",
		"Я работал с k8s и это pg",
		"",
		"the and is a",
		"deploy java service",
	}

	// Эталоны считаются однопоточно до запуска горутин.
	wantTokenize := make([][]string, len(tokenizeInputs))
	for i, in := range tokenizeInputs {
		wantTokenize[i] = tok.Tokenize(in)
	}
	wantCanonical := make([][]string, len(canonicalInputs))
	for i, in := range canonicalInputs {
		wantCanonical[i] = tok.Canonicalize(in)
	}
	wantStop := make([][]string, len(stopInputs))
	for i, in := range stopInputs {
		wantStop[i] = tok.RemoveStopWords(in)
	}
	wantProcess := make([][]string, len(processInputs))
	for i, in := range processInputs {
		wantProcess[i] = tok.Process(in)
	}

	// База для проверки, что Tokenizer не изменяется под нагрузкой.
	baseline := tok.Process("k8s and kube deploy")

	const goroutines = 100
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for it := 0; it < iterations; it++ {
				for i, in := range tokenizeInputs {
					if got := tok.Tokenize(in); !reflect.DeepEqual(got, wantTokenize[i]) {
						t.Errorf("Tokenize(%q): got %v, want %v", in, got, wantTokenize[i])
						return
					}
				}
				for i, in := range canonicalInputs {
					if got := tok.Canonicalize(in); !reflect.DeepEqual(got, wantCanonical[i]) {
						t.Errorf("Canonicalize(%v): got %v, want %v", in, got, wantCanonical[i])
						return
					}
				}
				for i, in := range stopInputs {
					if got := tok.RemoveStopWords(in); !reflect.DeepEqual(got, wantStop[i]) {
						t.Errorf("RemoveStopWords(%v): got %v, want %v", in, got, wantStop[i])
						return
					}
				}
				for i, in := range processInputs {
					if got := tok.Process(in); !reflect.DeepEqual(got, wantProcess[i]) {
						t.Errorf("Process(%q): got %v, want %v", in, got, wantProcess[i])
						return
					}
				}
			}
		}()
	}
	wg.Wait()

	// Tokenizer не изменился: Process одного входа стабилен до/после.
	if got := tok.Process("k8s and kube deploy"); !reflect.DeepEqual(got, baseline) {
		t.Errorf("Process после конкурентной нагрузки изменился: %v vs %v", baseline, got)
	}
}
