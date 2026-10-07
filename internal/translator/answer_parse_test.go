package translator

import "testing"

// TestParseAnswer covers the full format, a missing target separator, an empty
// target text, and the tricky case of the target separator appearing inside the
// answer text (the first occurrence splits, Source keeps the prefix).
func TestParseAnswer(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		srcTag    string
		tgtTag    string
		want      struct{ source, target string }
		wantFound bool
	}{
		{
			name:      "full format en/ru",
			input:     "EN: Redis is a cache | RU: Redis — это кэш",
			srcTag:    "EN",
			tgtTag:    "RU",
			want:      struct{ source, target string }{"EN: Redis is a cache", "Redis — это кэш"},
			wantFound: true,
		},
		{
			name:      "full format en/de parameterized tags",
			input:     "EN: A cache | DE: Ein Cache",
			srcTag:    "EN",
			tgtTag:    "DE",
			want:      struct{ source, target string }{"EN: A cache", "Ein Cache"},
			wantFound: true,
		},
		{
			name:      "no target separator keeps whole line as source",
			input:     "EN: Redis is a cache",
			srcTag:    "EN",
			tgtTag:    "RU",
			want:      struct{ source, target string }{"EN: Redis is a cache", ""},
			wantFound: false,
		},
		{
			name:      "empty target text",
			input:     "EN: Redis is a cache | RU:",
			srcTag:    "EN",
			tgtTag:    "RU",
			want:      struct{ source, target string }{"EN: Redis is a cache", ""},
			wantFound: true,
		},
		{
			name:      "target separator inside answer text splits on first occurrence",
			input:     "EN: use | RU: only for caches | RU: and nothing else",
			srcTag:    "EN",
			tgtTag:    "RU",
			want:      struct{ source, target string }{"EN: use", "only for caches | RU: and nothing else"},
			wantFound: true,
		},
		{
			name:      "foreign target tag is not a separator",
			input:     "EN: a cache | RU: кэш",
			srcTag:    "EN",
			tgtTag:    "DE",
			want:      struct{ source, target string }{"EN: a cache | RU: кэш", ""},
			wantFound: false,
		},
		{
			name:      "surrounding whitespace trimmed",
			input:     "  EN: hi | RU: привет  ",
			srcTag:    "EN",
			tgtTag:    "RU",
			want:      struct{ source, target string }{"EN: hi", "привет"},
			wantFound: true,
		},
		{
			name:      "empty input",
			input:     "",
			srcTag:    "EN",
			tgtTag:    "RU",
			want:      struct{ source, target string }{"", ""},
			wantFound: false,
		},
		{
			name:      "only source tag without separator",
			input:     "EN:",
			srcTag:    "EN",
			tgtTag:    "RU",
			want:      struct{ source, target string }{"EN:", ""},
			wantFound: false,
		},
		{
			name:      "only target separator with empty both sides",
			input:     "| RU:",
			srcTag:    "EN",
			tgtTag:    "RU",
			want:      struct{ source, target string }{"", ""},
			wantFound: true,
		},
		{
			name:      "whitespace-only input",
			input:     "   ",
			srcTag:    "EN",
			tgtTag:    "RU",
			want:      struct{ source, target string }{"", ""},
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := ParseAnswer(tt.input, tt.srcTag, tt.tgtTag)
			if found != tt.wantFound {
				t.Errorf("ParseAnswer(%q) found = %v, want %v", tt.input, found, tt.wantFound)
			}
			if got.Source != tt.want.source {
				t.Errorf("ParseAnswer(%q).Source = %q, want %q", tt.input, got.Source, tt.want.source)
			}
			if got.Target != tt.want.target {
				t.Errorf("ParseAnswer(%q).Target = %q, want %q", tt.input, got.Target, tt.want.target)
			}
		})
	}
}
