package stackdet

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// testFamilies — набор семейств для тестов; в проде приходит из
// manifest.tech_families.
func testFamilies() []Family {
	return []Family{
		{Name: "php", Display: "PHP", Members: []string{"php", "laravel", "symfony", "composer"}},
		{Name: "java", Display: "Java", Members: []string{"java", "spring", "hibernate", "jvm"}, Ambiguous: []string{"spring"}},
		{Name: "golang", Display: "Go", Members: []string{"golang"}},
	}
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func assertSet(t *testing.T, name string, got map[string]struct{}, want ...string) {
	t.Helper()
	sort.Strings(want)
	gotKeys := keys(got)
	if len(want) == 0 {
		want = nil
	}
	if len(gotKeys) == 0 {
		gotKeys = nil
	}
	if !reflect.DeepEqual(gotKeys, want) {
		t.Errorf("%s: got %v, want %v", name, gotKeys, want)
	}
}

func cloneStrings(in []string) []string {
	return append([]string(nil), in...)
}

// TestDetectUnambiguousMention проверяет, что имя семьи и однозначный член
// попадают и в Penalty, и в Named.
func TestDetectUnambiguousMention(t *testing.T) {
	tests := []struct {
		name    string
		q       []string
		penalty []string
		named   []string
		exempt  []string
	}{
		{name: "family name", q: []string{"php"}, penalty: []string{"php"}, named: []string{"php"}},
		{name: "unambiguous member laravel", q: []string{"laravel"}, penalty: []string{"php"}, named: []string{"php"}},
		{name: "unambiguous member composer", q: []string{"composer"}, penalty: []string{"php"}, named: []string{"php"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Detect(tt.q, nil, testFamilies())
			assertSet(t, "Penalty", s.Penalty, tt.penalty...)
			assertSet(t, "Named", s.Named, tt.named...)
			assertSet(t, "Exempt", s.Exempt, tt.exempt...)
		})
	}
}

// TestDetectAmbiguousMember проверяет, что неоднозначный член не попадает ни
// в Penalty, ни в Named, а только в Exempt своей семьи.
func TestDetectAmbiguousMember(t *testing.T) {
	s := Detect([]string{"spring"}, nil, testFamilies())
	assertSet(t, "Penalty", s.Penalty)
	assertSet(t, "Named", s.Named)
	assertSet(t, "Exempt", s.Exempt, "java")
}

// TestDetectAmbiguousAmbiguousOverridesMembersStr проверяет приоритет
// Ambiguous над Members: spring есть в Members java, но помечен неоднозначным.
func TestDetectAmbiguousAmbiguousOverridesMembersStr(t *testing.T) {
	fams := []Family{{
		Name:    "java",
		Members: []string{"java", "spring"},
		// spring продублирован в Ambiguous — побеждает Ambiguous.
		Ambiguous: []string{"spring"},
	}}
	s := Detect([]string{"spring"}, nil, fams)
	if len(s.Named) != 0 || len(s.Penalty) != 0 {
		t.Fatalf("spring must not be named: %+v", s)
	}
	assertSet(t, "Exempt", s.Exempt, "java")
}

// TestDetectNamedBeatsAmbiguous проверяет приоритет однозначного упоминания
// над неоднозначным в пределах одной семьи: вопрос «PHP или spring?» называет
// php однозначно, поэтому php идёт в Named, а java остаётся лишь в Exempt
// (spring так и не стал однозначным упоминанием).
func TestDetectNamedBeatsAmbiguous(t *testing.T) {
	s := Detect([]string{"php", "spring"}, nil, testFamilies())
	assertSet(t, "Penalty", s.Penalty, "php")
	assertSet(t, "Named", s.Named, "php")
	assertSet(t, "Exempt", s.Exempt, "java")
}

// TestDetectSessionUnion проверяет, что сессия целиком попадает в Penalty,
// но не в Named.
func TestDetectSessionUnion(t *testing.T) {
	s := Detect(nil, []string{"java"}, testFamilies())
	assertSet(t, "Penalty", s.Penalty, "java")
	assertSet(t, "Named", s.Named)
	assertSet(t, "Exempt", s.Exempt)
}

// TestDetectSessionPlusMention — «а на PHP?» в java-сессии: Penalty={java,php},
// Named={php}, java остаётся штрафным.
func TestDetectSessionPlusMention(t *testing.T) {
	s := Detect([]string{"php"}, []string{"java"}, testFamilies())
	assertSet(t, "Penalty", s.Penalty, "java", "php")
	assertSet(t, "Named", s.Named, "php")
	assertSet(t, "Exempt", s.Exempt)
}

// TestDetectUnknownWord — слово вне известных семей не даёт ничего.
func TestDetectUnknownWord(t *testing.T) {
	s := Detect([]string{"kubernetes", "kafka"}, nil, testFamilies())
	assertSet(t, "Penalty", s.Penalty)
	assertSet(t, "Named", s.Named)
	assertSet(t, "Exempt", s.Exempt)
}

// TestDetectEmptyInputs — пустые входы не паникуют и дают непустые (не nil)
// Sets.
func TestDetectEmptyInputs(t *testing.T) {
	for _, s := range []Sets{
		Detect(nil, nil, nil),
		Detect(nil, nil, testFamilies()),
		Detect([]string{}, []string{}, []Family{}),
	} {
		if s.Penalty == nil || s.Named == nil || s.Exempt == nil {
			t.Fatalf("Sets maps must be initialized: %+v", s)
		}
		if len(s.Penalty)+len(s.Named)+len(s.Exempt) != 0 {
			t.Fatalf("empty inputs must produce empty sets: %+v", s)
		}
	}
}

// TestDetectDoesNotMutateInputs — Detect не мутирует входные срезы.
func TestDetectDoesNotMutateInputs(t *testing.T) {
	q := []string{"php", "spring", "kubernetes"}
	session := []string{"java"}
	fams := testFamilies()

	qCopy := cloneStrings(q)
	sessionCopy := cloneStrings(session)
	famsCopy := make([]Family, len(fams))
	for i, f := range fams {
		famsCopy[i] = Family{
			Name:      f.Name,
			Display:   f.Display,
			Members:   cloneStrings(f.Members),
			Ambiguous: cloneStrings(f.Ambiguous),
		}
	}

	Detect(q, session, fams)

	if !reflect.DeepEqual(q, qCopy) {
		t.Errorf("questionTokens mutated: %v → %v", qCopy, q)
	}
	if !reflect.DeepEqual(session, sessionCopy) {
		t.Errorf("session mutated: %v → %v", sessionCopy, session)
	}
	if !reflect.DeepEqual(fams, famsCopy) {
		t.Errorf("families mutated:\n got %+v\nwant %+v", fams, famsCopy)
	}
}

// TestMultiplierTable — сценарные кейсы из плана (M3).
func TestMultiplierTable(t *testing.T) {
	p := DefaultParams()
	tests := []struct {
		name      string
		q         []string
		session   []string
		factStack []string
		wantMult  float64
		wantIn    string // подстрока reason, "" — reason пустой
	}{
		{
			name:      "1. java session, php fact → penalty",
			session:   []string{"java"},
			factStack: []string{"php"},
			wantMult:  0.3,
			wantIn:    "penalty: stack [php] ∉ {java}",
		},
		{
			name:      "1b. java session, java fact → session bonus",
			session:   []string{"java"},
			factStack: []string{"java"},
			wantMult:  1.25,
			wantIn:    "session",
		},
		{
			name:      "2. php named in java session, php fact → named bonus",
			q:         []string{"php"},
			session:   []string{"java"},
			factStack: []string{"php"},
			wantMult:  1.5,
			wantIn:    "bonus: named php",
		},
		{
			name:      "2b. php named in java session, java fact → no bonus",
			q:         []string{"php"},
			session:   []string{"java"},
			factStack: []string{"java"},
			wantMult:  1.0,
		},
		{
			name:      "3. laravel to spring, empty session → php fact named (laravel)",
			q:         []string{"laravel", "spring"},
			factStack: []string{"php"},
			wantMult:  1.5,
			wantIn:    "bonus: named php",
		},
		{
			name:      "3b. laravel to spring, empty session → java exempt",
			q:         []string{"laravel", "spring"},
			factStack: []string{"java"},
			wantMult:  1.0,
		},
		{
			name:      "4. go about, empty session → no penalty",
			q:         []string{"about"},
			factStack: []string{"php"},
			wantMult:  1.0,
		},
		{
			name:      "5. mixed [java, php] in java session → partial → 1.0",
			session:   []string{"java"},
			factStack: []string{"java", "php"},
			wantMult:  1.0,
		},
		{
			name:      "6a. empty factStack → 1.0",
			session:   []string{"java"},
			factStack: nil,
			wantMult:  1.0,
		},
		{
			name:      "6b. unknown mention → no penalty",
			q:         []string{"kubernetes"},
			factStack: []string{"php"},
			wantMult:  1.0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Detect(tt.q, tt.session, testFamilies())
			mult, reason := Multiplier(tt.factStack, s, p)
			if mult != tt.wantMult {
				t.Fatalf("mult: got %v, want %v (reason %q)", mult, tt.wantMult, reason)
			}
			if tt.wantIn == "" {
				if reason != "" {
					t.Errorf("reason: got %q, want empty", reason)
				}
				return
			}
			if !strings.Contains(reason, tt.wantIn) {
				t.Errorf("reason: got %q, want substring %q", reason, tt.wantIn)
			}
		})
	}
}

// TestMultiplierReasonNamesStack — reason содержит имя стека (п.7 задания).
func TestMultiplierReasonNamesStack(t *testing.T) {
	s := Detect(nil, []string{"java"}, testFamilies())
	_, reason := Multiplier([]string{"php"}, s, DefaultParams())
	if !strings.Contains(reason, "php") {
		t.Errorf("penalty reason must name the fact stack: %q", reason)
	}
	if !strings.Contains(reason, "java") {
		t.Errorf("penalty reason must name the target stack: %q", reason)
	}

	s = Detect([]string{"laravel"}, nil, testFamilies())
	_, reason = Multiplier([]string{"php"}, s, DefaultParams())
	if !strings.Contains(reason, "php") {
		t.Errorf("bonus reason must name the named stack: %q", reason)
	}
}

// TestMultiplierReasonNonEmpty — при любом mult ≠ 1.0 reason непуст.
func TestMultiplierReasonNonEmpty(t *testing.T) {
	p := DefaultParams()
	cases := []struct {
		q         []string
		session   []string
		factStack []string
	}{
		{[]string{"php"}, nil, []string{"php"}},
		{[]string{"php"}, []string{"java"}, []string{"php"}},
		{nil, []string{"java"}, []string{"java"}},
		{nil, []string{"java"}, []string{"php", "golang"}},
	}
	for i, c := range cases {
		s := Detect(c.q, c.session, testFamilies())
		mult, reason := Multiplier(c.factStack, s, p)
		if mult != 1.0 && reason == "" {
			t.Errorf("case %d: mult=%v but reason is empty", i, mult)
		}
	}
}

// TestMultiplierEmptyPenalty — без сессии и без упоминаний штрафов нет.
func TestMultiplierEmptyPenalty(t *testing.T) {
	s := Detect([]string{"service", "about"}, nil, testFamilies())
	mult, reason := Multiplier([]string{"php"}, s, DefaultParams())
	if mult != 1.0 || reason != "" {
		t.Errorf("got (%v, %q), want (1, \"\")", mult, reason)
	}
}

// TestMultiplierEmptySets — нулевые Sets (не через Detect) не паникуют.
func TestMultiplierEmptySets(t *testing.T) {
	mult, reason := Multiplier([]string{"php"}, Sets{}, DefaultParams())
	if mult != 1.0 || reason != "" {
		t.Errorf("got (%v, %q), want (1, \"\")", mult, reason)
	}
}

// TestMultiplierZeroParams — Params{} трактуется как дефолт, а не обнуляет
// скоринг.
func TestMultiplierZeroParams(t *testing.T) {
	s := Detect(nil, []string{"java"}, testFamilies())
	if mult, _ := Multiplier([]string{"php"}, s, Params{}); mult != 0.3 {
		t.Errorf("penalty with zero params: got %v, want 0.3", mult)
	}
	s = Detect([]string{"php"}, nil, testFamilies())
	if mult, _ := Multiplier([]string{"php"}, s, Params{}); mult != 1.5 {
		t.Errorf("bonus with zero params: got %v, want 1.5", mult)
	}
	s = Detect(nil, []string{"java"}, testFamilies())
	if mult, _ := Multiplier([]string{"java"}, s, Params{}); mult != 1.25 {
		t.Errorf("session bonus with zero params: got %v, want 1.25", mult)
	}
}

// TestMultiplierCustomParams — явные коэффициенты используются как есть.
func TestMultiplierCustomParams(t *testing.T) {
	p := Params{Penalty: 0.5, Bonus: 2.0, Session: 1.1}
	s := Detect(nil, []string{"java"}, testFamilies())
	if mult, _ := Multiplier([]string{"php"}, s, p); mult != 0.5 {
		t.Errorf("custom penalty: got %v, want 0.5", mult)
	}
	s = Detect([]string{"php"}, nil, testFamilies())
	if mult, _ := Multiplier([]string{"php"}, s, p); mult != 2.0 {
		t.Errorf("custom bonus: got %v, want 2.0", mult)
	}
	s = Detect(nil, []string{"java"}, testFamilies())
	if mult, _ := Multiplier([]string{"java"}, s, p); mult != 1.1 {
		t.Errorf("custom session: got %v, want 1.1", mult)
	}
}

// TestMultiplierNamedBeatsSession — session-таргет не даёт бонуса, если стек
// назван в вопросе (п.3 приоритетнее п.4).
func TestMultiplierNamedBeatsSession(t *testing.T) {
	// Сессия java, вопрос называет java: Named={java}, значит java-факт
	// получает named-бонус 1.5, а не session 1.25.
	s := Detect([]string{"java"}, []string{"java"}, testFamilies())
	mult, reason := Multiplier([]string{"java"}, s, DefaultParams())
	if mult != 1.5 {
		t.Errorf("named stack in session: got %v, want 1.5 (%q)", mult, reason)
	}
}

// TestMultiplierSubsetSemantics — session-бонус только при полном вхождении
// стека факта в сессию.
func TestMultiplierSubsetSemantics(t *testing.T) {
	s := Detect(nil, []string{"java", "php"}, testFamilies())
	if mult, _ := Multiplier([]string{"java"}, s, DefaultParams()); mult != 1.25 {
		t.Errorf("single stack ⊆ session: got %v, want 1.25", mult)
	}
	if mult, _ := Multiplier([]string{"java", "php"}, s, DefaultParams()); mult != 1.25 {
		t.Errorf("both ⊆ session: got %v, want 1.25", mult)
	}
	if mult, _ := Multiplier([]string{"java", "php", "golang"}, s, DefaultParams()); mult != 1.0 {
		t.Errorf("partial intersection: got %v, want 1.0", mult)
	}
}

// TestMultiplierDoesNotMutateFactStack — Multiplier не мутирует вход.
func TestMultiplierDoesNotMutateFactStack(t *testing.T) {
	stack := []string{"php", "java"}
	copyStack := cloneStrings(stack)
	s := Detect(nil, []string{"java"}, testFamilies())
	Multiplier(stack, s, DefaultParams())
	if !reflect.DeepEqual(stack, copyStack) {
		t.Errorf("factStack mutated: %v → %v", copyStack, stack)
	}
}

// TestDefaultParams — дефолтные коэффициенты из плана.
func TestDefaultParams(t *testing.T) {
	p := DefaultParams()
	if p.Penalty != 0.3 || p.Bonus != 1.5 || p.Session != 1.25 {
		t.Errorf("DefaultParams: got %+v", p)
	}
}
