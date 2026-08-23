package candidatecontext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// LoadManifest читает и парсит manifest.json.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("candidatecontext: чтение manifest %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("candidatecontext: парсинг manifest %s: %w", path, err)
	}
	return &m, nil
}

// LoadIndex читает и парсит index.json.
func LoadIndex(path string) (*Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("candidatecontext: чтение index %s: %w", path, err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("candidatecontext: парсинг index %s: %w", path, err)
	}
	return &idx, nil
}

// IsIndexVersionCompatible сообщает, совместим ли формат index с текущим
// билдером/токенизатором. Несовместимый index требует пересборки.
func IsIndexVersionCompatible(idx *Index) bool {
	return idx != nil && idx.Version == IndexVersion
}

// CheckIndexFresh проверяет, актуален ли index относительно manifest.json и
// source-файлов секций. Работает read-only: ничего не строит, только сравнивает
// SHA256/size. Возвращает (true, "") если index свежий и совместимый, иначе
// (false, reason) с человекочитаемой причиной устаревания.
func CheckIndexFresh(manifest *Manifest, index *Index, root string) (fresh bool, reason string) {
	if index == nil {
		return false, "index отсутствует"
	}
	if !IsIndexVersionCompatible(index) {
		return false, "несовместимая версия index"
	}

	manifestRaw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return false, "не удалось прочитать manifest.json"
	}
	manifestSum := sha256.Sum256(manifestRaw)
	if hex.EncodeToString(manifestSum[:]) != index.ManifestSHA256 {
		return false, "manifest изменён"
	}

	metaByPath := make(map[string]FileMeta, len(index.Files))
	for _, m := range index.Files {
		metaByPath[m.Path] = m
	}

	for _, section := range manifest.Sections {
		rel := "sections/" + section.ID + ".md"
		meta, ok := metaByPath[rel]
		if !ok {
			return false, "новый source-файл для section " + section.ID
		}

		raw, err := os.ReadFile(filepath.Join(root, "sections", section.ID+".md"))
		if err != nil {
			if os.IsNotExist(err) {
				return false, "отсутствует source-файл " + rel
			}
			return false, "не удалось прочитать " + rel
		}
		sum := sha256.Sum256(raw)
		if int64(len(raw)) != meta.Size || hex.EncodeToString(sum[:]) != meta.SHA256 {
			return false, "source-файл " + rel + " изменён"
		}
	}

	return true, ""
}

// LoadCandidateContext инкапсулирует полный lifecycle загрузки candidate
// context: читает manifest, проверяет свежесть index и при необходимости
// пересобирает его. Возвращает оба значения (manifest и index).
func LoadCandidateContext(root string) (*Manifest, *Index, error) {
	manifestPath := filepath.Join(root, "manifest.json")
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return nil, nil, fmt.Errorf("candidatecontext: load manifest: %w", err)
	}

	indexPath := filepath.Join(root, "index.json")
	idx, loadErr := LoadIndex(indexPath)

	fresh := false
	reason := "index отсутствует или не читается"
	if loadErr == nil {
		fresh, reason = CheckIndexFresh(manifest, idx, root)
	} else {
		reason = fmt.Sprintf("index не загружен: %v", loadErr)
	}

	if fresh {
		return manifest, idx, nil
	}

	slog.Warn("candidatecontext: индекс устарел, пересборка", "reason", reason)

	idx, err = BuildIndex(manifest, root)
	if err != nil {
		return nil, nil, fmt.Errorf("candidatecontext: rebuild index: %w", err)
	}
	if err := SaveIndex(idx, indexPath); err != nil {
		return nil, nil, fmt.Errorf("candidatecontext: save index: %w", err)
	}
	return manifest, idx, nil
}
