package candidatecontext

import (
	"reflect"
	"strings"
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
		{"internal hyphen", "low-latency", []string{"low-latency", "low", "latency"}},
		{"slash split", "php/laravel", []string{"php", "laravel"}},
		{"backslash split", "php\\laravel", []string{"php", "laravel"}},
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

// --- Golden (M5) ---
//
// ВНИМАНИЕ: любое изменение ожиданий в tokenGolden — это изменение ПОВЕДЕНИЯ
// токенизатора. Тогда обязательно bump TokenizerVersion (tokenize.go), иначе
// уже собранные индексы останутся «свежими», хотя их токены посчитаны по
// старой семантике, а вопросы будут канонизироваться по новой.
//
// ASR-форм из логов в списке подстановок нет: в logs/ (11 session_*.csv +
// fatal-*.log) не встретилось ни одной технической формы (см. asrSubstitutions).
// Найденные формы следует добавлять и туда, и в этот golden.

// tokenGolden — таблица «строка → ожидаемые токены» после Process.
var tokenGolden = []struct {
	name  string
	input string
	want  []string
}{
	// Сплит по "/" и "\\": симметрично для вопроса и фактов.
	{"php-laravel slash", "PHP/Laravel", []string{"php", "laravel"}},
	{"php yii2 mysql", "PHP (Yii2) / MySQL", []string{"php", "yii2", "mysql"}},
	{"backslash split", "PHP\\Laravel", []string{"php", "laravel"}},
	{"tcp ip", "TCP/IP", []string{"tcp", "ip"}},

	// "&" внутри слова сохраняется: R&D — один токен.
	{"r and d", "R&D", []string{"r&d"}},

	// Go-контекст → golang (не стоп-слово). Предметные слова фразы
	// сохраняются: "go microservices" даёт и golang, и microservices —
	// иначе вопрос про microservices architecture переставал находить факт.
	{"go microservices", "Go microservices", []string{"golang", "microservices"}},
	{"go language", "go language", []string{"golang"}},
	{"go developer", "go developer", []string{"golang"}},
	{"in go language", "In go language", []string{"golang"}},
	{"with go microservices", "with go microservices", []string{"golang", "microservices"}},
	{"golang", "Golang", []string{"golang"}},

	// Дефис: целое и части (ASR даёт "high load").
	{"high load", "high load", []string{"high", "load"}},
	{"high-load", "high-load", []string{"high-load", "high", "load"}},
	{"service-level", "service-level agreement", []string{"service-level", "service", "level", "agreement"}},

	// Спецтермины: точное совпадение поля → канонический токен.
	{"c++", "C++", []string{"cpp"}},
	{"c++ punctuation", "c++ developer", []string{"cpp", "developer"}},
	{"c sharp", "C#", []string{"csharp"}},
	{"dotnet", ".NET", []string{"dotnet"}},
	{"dotnet core", ".NET Core", []string{"dotnet", "core"}},
	{"special terms list", "c#, .net, asp.net", []string{"csharp", "dotnet", "asp.net"}},

	// Спецтермины НЕ затрагивают составные имена.
	{"asp.net", "asp.net", []string{"asp.net"}},
	{"node.js", "node.js", []string{"node.js"}},
	{"c++20", "c++20", []string{"c++20"}},

	// Стоп-слова удаляются, в т.ч. "go" без go-контекста.
	{"stop words", "the and in", nil},
	{"stop word go", "go", nil},

	// Негативные golden-строки deny-list: "go" не превращается в golang.
	{"go live", "go live", []string{"live"}},
	{"go ahead", "go ahead", []string{"ahead"}},
	{"go through", "go through", nil},
	{"go about", "go about", nil},
	{"go to", "go to", nil},
	{"go-to-market", "go-to-market", []string{"go-to-market", "market"}},
	{"with go-to-market", "with go-to-market", []string{"go-to-market", "market"}},
	{"go with the first option", "to go with the first option", []string{"first", "option"}},
	{"experience with go", "experience with go", []string{"experience", "golang"}},
	{"experience with go question", "what is your experience with go?", []string{"experience", "golang"}},
	{"in go", "In go", []string{"golang"}},
	{"with go", "with go", []string{"golang"}},

	// Пустые токены отбрасываются: "|", "—", "&".
	{"punctuation only", "a | b — c & d", []string{"b", "c", "d"}},
	{"dashes only", "— —", nil},
	{"empty", "", nil},
	{"spaces", "   	\n ", nil},
}

func TestProcessGolden(t *testing.T) {
	tok, err := NewTokenizer(nil)
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	for _, tc := range tokenGolden {
		t.Run(tc.name, func(t *testing.T) {
			got := tok.Process(tc.input)
			// nil и пустой слайс считаются одинаково: пустой результат.
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Process(%q) = %v, want %v\n"+
					"Если это ожидаемое изменение поведения — bump TokenizerVersion "+
					"(сейчас %q).", tc.input, got, tc.want, TokenizerVersion)
			}
		})
	}
}

func TestSubstituteBoundaries(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"c++", "cpp"},
		{"c#", "csharp"},
		{".net", "dotnet"},
		{"asp.net", "asp.net"},
		{"node.js", "node.js"},
		{"c++20", "c++20"},
		{"csharp and .net", "csharp and dotnet"},
		{"go-to-market", "go-to-market"},
		{"with go-to-market", "with go-to-market"},
		{"go live", "go live"},
		{"go ahead", "go ahead"},
		{"go through", "go through"},
		{"go about", "go about"},
		{"to go with the first option", "to go with the first option"},
		{"go language", "golang"},
		{"go developer", "golang"},
		{"go microservices", "golang microservices"},
		{"in go", "golang"},
		{"with go", "golang"},
	}
	tok, err := NewTokenizer(nil)
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	for _, tc := range cases {
		if got := tok.Substitute(tc.in); got != tc.want {
			t.Errorf("Substitute(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestProcessOrder фиксирует порядок Normalize → Substitute → Tokenize →
// RemoveStopWords → Canonicalize: подстановка видит сырую строку (до edge-trim
// и до удаления стоп-слов), а канонизация — уже после подстановки.
func TestProcessOrder(t *testing.T) {
	tok, err := NewTokenizer(map[string][]string{"framework": {"tool"}})
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	// "GO Developer" → Normalize → подстановка golang.
	if got := tok.Process("GO Developer"); !reflect.DeepEqual(got, []string{"golang"}) {
		t.Errorf("Process = %v, want [golang]", got)
	}
	// Подстановка идёт до Canonicalize и даёт участника canonical-группы.
	if got := tok.Process("Tool"); !reflect.DeepEqual(got, []string{"framework"}) {
		t.Errorf("Process = %v, want [framework]", got)
	}
}

func TestTokenizerVersionNotEmpty(t *testing.T) {
	if strings.TrimSpace(TokenizerVersion) == "" {
		t.Fatal("TokenizerVersion пуст: версия обязана быть в индексе и в freshness")
	}
}
