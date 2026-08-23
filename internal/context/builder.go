package candidatecontext

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BuildIndex строит runtime retrieval index для candidate context.
//
// root — путь к каталогу candidate_context (содержит manifest.json, sections/).
// Для каждой секции из manifest ожидается source-файл sections/<id>.md, в
// котором факты размечены HTML-комментариями вида:
//
//	<!-- fact
//	{
//	  "id": "...",
//	  "section": "...",
//	  "title": "...",
//	  "keywords": [...],
//	  "aliases": [...]
//	}
//	-->
//
// Границы факта: Start = сразу после закрывающего "-->" маркера, End = начало
// "<!--" следующего маркера (или конец файла). Текст до первого маркера вне
// фактов. Keywords/Aliases/ContentTokens канонизируются тем же Tokenizer'ом,
// что и runtime retriever, поэтому их семантика совпадает.
func BuildIndex(manifest *Manifest, root string) (*Index, error) {
	tokenizer, err := NewTokenizer(manifest.GlobalAliases)
	if err != nil {
		return nil, fmt.Errorf("candidatecontext: build index: %w", err)
	}

	manifestPath := filepath.Join(root, "manifest.json")
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("candidatecontext: build index: чтение manifest: %w", err)
	}
	manifestSHA := sha256.Sum256(manifestRaw)

	sectionIDs := make(map[string]struct{}, len(manifest.Sections))
	for _, s := range manifest.Sections {
		if s.ID == "" {
			return nil, fmt.Errorf("candidatecontext: build index: пустой section id")
		}
		if _, dup := sectionIDs[s.ID]; dup {
			return nil, fmt.Errorf("candidatecontext: build index: дубликат section id %q", s.ID)
		}
		sectionIDs[s.ID] = struct{}{}
	}

	index := &Index{
		Version:        IndexVersion,
		ManifestSHA256: hex.EncodeToString(manifestSHA[:]),
		Files:          make([]FileMeta, 0, len(manifest.Sections)),
		Facts:          make([]Fact, 0),
	}
	seenFactIDs := make(map[string]struct{})

	for _, section := range manifest.Sections {
		sourcePath := filepath.Join(root, "sections", section.ID+".md")
		raw, err := os.ReadFile(sourcePath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("candidatecontext: build index: отсутствует source-файл для section %q", section.ID)
			}
			return nil, fmt.Errorf("candidatecontext: build index: чтение section %q: %w", section.ID, err)
		}

		fileRel := filepath.ToSlash(filepath.Join("sections", section.ID+".md"))
		fileSHA := sha256.Sum256(raw)
		index.Files = append(index.Files, FileMeta{
			Path:   fileRel,
			Size:   int64(len(raw)),
			SHA256: hex.EncodeToString(fileSHA[:]),
		})

		facts, err := parseFactMarkers(raw, tokenizer)
		if err != nil {
			return nil, fmt.Errorf("candidatecontext: build index: %w", err)
		}
		for _, f := range facts {
			if _, ok := sectionIDs[f.Section]; !ok {
				return nil, fmt.Errorf("candidatecontext: build index: section %q отсутствует в manifest", f.Section)
			}
			if _, dup := seenFactIDs[f.ID]; dup {
				return nil, fmt.Errorf("candidatecontext: build index: дубликат fact id %q", f.ID)
			}
			seenFactIDs[f.ID] = struct{}{}
			f.File = fileRel
			index.Facts = append(index.Facts, f)
		}
	}

	return index, nil
}

// factMarker — JSON-содержимое одного маркера "<!-- fact ... -->".
type factMarker struct {
	ID       string   `json:"id"`
	Section  string   `json:"section"`
	Title    string   `json:"title"`
	Keywords []string `json:"keywords"`
	Aliases  []string `json:"aliases"`
}

// parseFactMarkers находит все маркеры "<!-- fact ... -->" в raw и строит
// факты (без File и без валидации section против manifest — это делает
// BuildIndex). Возвращает факты в порядке возрастания offset.
func parseFactMarkers(raw []byte, tokenizer *Tokenizer) ([]Fact, error) {
	const markerStart = "<!-- fact"
	const markerClose = "-->"

	type positioned struct {
		start int // offset "<!-- fact"
		end   int // offset сразу после "-->" (граница Start факта)
		json  []byte
	}

	var markers []positioned
	pos := 0
	for {
		idx := bytes.Index(raw[pos:], []byte(markerStart))
		if idx < 0 {
			break
		}
		start := pos + idx
		closeIdx := bytes.Index(raw[start:], []byte(markerClose))
		if closeIdx < 0 {
			return nil, fmt.Errorf("маркер факта без закрывающего %q", markerClose)
		}
		closeStart := start + closeIdx
		markers = append(markers, positioned{
			start: start,
			end:   closeStart + len(markerClose),
			json:  raw[start+len(markerStart) : closeStart],
		})
		pos = closeStart + len(markerClose)
	}

	if len(markers) == 0 {
		return nil, fmt.Errorf("отсутствуют fact markers в source-файле")
	}

	facts := make([]Fact, 0, len(markers))
	for i, m := range markers {
		var fm factMarker
		if err := json.Unmarshal(bytes.TrimSpace(m.json), &fm); err != nil {
			return nil, fmt.Errorf("парсинг fact marker: %w", err)
		}
		fm.ID = strings.TrimSpace(fm.ID)
		fm.Section = strings.TrimSpace(fm.Section)
		if fm.ID == "" {
			return nil, fmt.Errorf("fact marker без id")
		}
		if fm.Section == "" {
			return nil, fmt.Errorf("fact marker без section")
		}

		start := int64(m.end)
		var end int64
		if i+1 < len(markers) {
			end = int64(markers[i+1].start)
		} else {
			end = int64(len(raw))
		}
		body := raw[start:end]

		keywords := make([]string, 0, len(fm.Keywords))
		for _, k := range fm.Keywords {
			keywords = append(keywords, tokenizer.Process(k)...)
		}
		aliases := make([]string, 0, len(fm.Aliases))
		for _, a := range fm.Aliases {
			aliases = append(aliases, tokenizer.Process(a)...)
		}

		facts = append(facts, Fact{
			ID:            fm.ID,
			Section:       fm.Section,
			Start:         start,
			End:           end,
			Title:         fm.Title,
			Keywords:      keywords,
			Aliases:       aliases,
			ContentTokens: tokenizer.Process(string(body)),
		})
	}
	return facts, nil
}

// tempFile — минимальный seam над *os.File для тестирования SaveIndex.
// *os.File удовлетворяет этому интерфейсу (Write/Close/Name).
type tempFile interface {
	Write([]byte) (int, error)
	Close() error
	Name() string
}

// createTempFile и renameFile — package-level seam'ы, переопределяемые в тестах
// для инъекции ошибок создания/записи/закрытия/переименования.
var createTempFile = func(dir, pattern string) (tempFile, error) {
	return os.CreateTemp(dir, pattern)
}

var renameFile = os.Rename

// SaveIndex атомарно записывает index в path (через temp file + rename в той
// же директории), не повреждая существующий target при ошибке. Leftover temp
// файлы удаляются при любой ошибке.
func SaveIndex(index *Index, path string) error {
	if index == nil {
		return fmt.Errorf("candidatecontext: save index: nil index")
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("candidatecontext: save index: marshal: %w", err)
	}

	tmp, err := createTempFile(filepath.Dir(path), ".index-*.tmp")
	if err != nil {
		return fmt.Errorf("candidatecontext: save index: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op после успешного rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("candidatecontext: save index: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("candidatecontext: save index: close: %w", err)
	}
	if err := renameFile(tmpName, path); err != nil {
		return fmt.Errorf("candidatecontext: save index: rename: %w", err)
	}
	return nil
}
