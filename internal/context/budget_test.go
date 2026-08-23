package candidatecontext

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mastererik/translator/internal/common"
)

func TestBudgetProfileWithinLimit(t *testing.T) {
	b := NewBudgeter(1000, 1000)
	profile := "Erik Ivanov — Staff Software Engineer"
	got := b.Budget(profile, nil)
	if got.Profile != profile {
		t.Errorf("Profile: got %q, want %q", got.Profile, profile)
	}
	if len(got.Facts) != 0 {
		t.Errorf("Facts: got %d, want 0", len(got.Facts))
	}
}

func TestBudgetProfileTruncatedUTF8Safe(t *testing.T) {
	// 2 tokens = 8 bytes. "abc" (3 ASCII bytes) + "Привет" (6 Cyrillic bytes
	// = 12 bytes). Byte 8 lands in the middle of a multi-byte rune, so a naive
	// byte slice would corrupt UTF-8.
	b := NewBudgeter(1000, 2)
	profile := "abcПривет"
	got := b.Budget(profile, nil)

	if !utf8.ValidString(got.Profile) {
		t.Fatalf("truncated profile is not valid UTF-8: %q", got.Profile)
	}
	if !strings.HasPrefix(profile, got.Profile) {
		t.Errorf("truncated profile %q is not a prefix of %q", got.Profile, profile)
	}
	if tk := common.EstimateTokens(got.Profile); tk > 2 {
		t.Errorf("truncated profile exceeds budget: %d tokens", tk)
	}
	// 8-byte cut rolls back to the end of the "р" rune at byte 7.
	if got.Profile != "abcПр" {
		t.Errorf("truncated profile: got %q, want %q", got.Profile, "abcПр")
	}
}

func TestBudgetProfileRollsBackToSpace(t *testing.T) {
	// 3 tokens = 12 bytes. Cut lands on "and"; rolls back to the space after
	// "world", keeping whole words.
	b := NewBudgeter(1000, 3)
	got := b.Budget("hello world and more", nil)
	if got.Profile != "hello world" {
		t.Errorf("Profile: got %q, want %q", got.Profile, "hello world")
	}
}

func TestBudgetAllFactsFit(t *testing.T) {
	b := NewBudgeter(1000, 1000)
	results := []RetrievalResult{
		{FactID: "a", SectionID: "s", Score: 3.0, Content: "fact a"},
		{FactID: "b", SectionID: "s", Score: 2.0, Content: "fact b"},
		{FactID: "c", SectionID: "s", Score: 1.0, Content: "fact c"},
	}
	got := b.Budget("", results)
	if len(got.Facts) != 3 {
		t.Fatalf("Facts: got %d, want 3", len(got.Facts))
	}
	for i, want := range []string{"a", "b", "c"} {
		if got.Facts[i].FactID != want {
			t.Errorf("Facts[%d].FactID = %q, want %q (order not preserved)", i, got.Facts[i].FactID, want)
		}
	}
}

func TestBudgetSkipsOversizedFact(t *testing.T) {
	// 10 tokens = 40 bytes. The big fact (100 bytes) does not fit and must be
	// skipped; the smaller fact is then included whole.
	b := NewBudgeter(10, 1000)
	results := []RetrievalResult{
		{FactID: "big", Content: strings.Repeat("x", 100)},
		{FactID: "small", Content: "small"}, // 5 bytes = 2 tokens
	}
	got := b.Budget("", results)
	if len(got.Facts) != 1 {
		t.Fatalf("Facts: got %d, want 1", len(got.Facts))
	}
	if got.Facts[0].FactID != "small" {
		t.Errorf("Facts[0].FactID = %q, want %q", got.Facts[0].FactID, "small")
	}
	if got.Facts[0].Content != "small" {
		t.Errorf("fact must be included whole, got %q", got.Facts[0].Content)
	}
}

func TestBudgetFallbackTruncatesTop1(t *testing.T) {
	// 4 tokens = 16 bytes. Every fact is oversized, so the top-1 must be
	// truncated to the fact budget.
	b := NewBudgeter(4, 1000)
	results := []RetrievalResult{
		{FactID: "top", Content: strings.Repeat("y", 100)},
		{FactID: "second", Content: strings.Repeat("z", 100)},
	}
	got := b.Budget("", results)
	if len(got.Facts) != 1 {
		t.Fatalf("Facts: got %d, want 1", len(got.Facts))
	}
	if got.Facts[0].FactID != "top" {
		t.Errorf("Facts[0].FactID = %q, want %q", got.Facts[0].FactID, "top")
	}
	if tk := common.EstimateTokens(got.Facts[0].Content); tk > 4 {
		t.Errorf("fallback content exceeds budget: %d tokens", tk)
	}
	if got.Facts[0].Content != strings.Repeat("y", 16) {
		t.Errorf("fallback content: got %d bytes, want 16", len(got.Facts[0].Content))
	}
}

func TestBudgetEmptyResults(t *testing.T) {
	b := NewBudgeter(100, 100)
	got := b.Budget("profile", nil)
	if len(got.Facts) != 0 {
		t.Errorf("Facts: got %d, want 0", len(got.Facts))
	}
	if got.Profile != "profile" {
		t.Errorf("Profile: got %q, want %q", got.Profile, "profile")
	}
}

func TestBudgetZeroFactBudget(t *testing.T) {
	// A zero fact budget cannot hold any non-empty fact; the fallback must
	// truncate the top-1 to nothing rather than panic or emit oversize content.
	b := NewBudgeter(0, 100)
	results := []RetrievalResult{
		{FactID: "a", Content: "hello"},
		{FactID: "b", Content: "world"},
	}
	got := b.Budget("", results)
	if len(got.Facts) != 1 {
		t.Fatalf("Facts: got %d, want 1", len(got.Facts))
	}
	if got.Facts[0].Content != "" {
		t.Errorf("fallback content with zero budget: got %q, want empty", got.Facts[0].Content)
	}
}

func TestBudgetSeparateBudgets(t *testing.T) {
	// A large profile must not consume the fact budget, and vice versa.
	b := NewBudgeter(100, 5) // 100 tokens facts, 5 tokens profile
	profile := "a very long profile text that far exceeds the profile budget"
	results := []RetrievalResult{
		{FactID: "f1", Content: strings.Repeat("f", 100)}, // 25 tokens
		{FactID: "f2", Content: strings.Repeat("g", 100)}, // 25 tokens
	}
	got := b.Budget(profile, results)

	if tk := common.EstimateTokens(got.Profile); tk > 5 {
		t.Errorf("profile exceeds its budget: %d tokens", tk)
	}
	if got.Profile == profile {
		t.Errorf("profile was not truncated")
	}
	if len(got.Facts) != 2 {
		t.Fatalf("Facts: got %d, want 2 (big profile must not eat fact budget)", len(got.Facts))
	}
}
