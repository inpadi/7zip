package archivefmt

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/inpadi/7zip/internal/archive7z"
)

type sourceFile struct {
	path string
	name string
	info fs.FileInfo
}

func collectSources(sources []string, archive string, recursive bool) ([]sourceFile, error) {
	archiveAbs, err := filepath.Abs(archive)
	if err != nil {
		return nil, err
	}
	files := make(map[string]sourceFile)
	for _, source := range sources {
		matches := []string{source}
		if strings.ContainsAny(source, "*?") {
			matches, err = sourceMatches(source, recursive)
			if err != nil {
				return nil, fmt.Errorf("invalid input wildcard %q: %w", source, err)
			}
			if len(matches) == 0 {
				return nil, fmt.Errorf("no input files match %q", source)
			}
		}
		for _, match := range matches {
			if err := collectSource(match, archiveAbs, files); err != nil {
				return nil, err
			}
		}
	}
	result := make([]sourceFile, 0, len(files))
	for _, file := range files {
		result = append(result, file)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result, nil
}

func sourceMatches(pattern string, recursive bool) ([]string, error) {
	absPattern, err := filepath.Abs(pattern)
	if err != nil {
		return nil, err
	}
	base := absPattern
	for strings.ContainsAny(base, "*?") {
		parent := filepath.Dir(base)
		if parent == base {
			break
		}
		base = parent
	}
	relPattern, err := filepath.Rel(base, absPattern)
	if err != nil {
		return nil, err
	}
	var matches []string
	err = filepath.WalkDir(base, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(base, name)
		if err != nil {
			return err
		}
		if sourceWildcardPathMatch(filepath.ToSlash(relPattern), filepath.ToSlash(rel), recursive) {
			matches = append(matches, name)
		}
		return nil
	})
	return matches, err
}

func sourceWildcardPathMatch(pattern, name string, recursive bool) bool {
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	nameParts := strings.Split(strings.Trim(name, "/"), "/")
	if recursive {
		if len(patternParts) > len(nameParts) {
			return false
		}
		nameParts = nameParts[len(nameParts)-len(patternParts):]
	}
	if len(patternParts) != len(nameParts) {
		return false
	}
	for i := range patternParts {
		if !sourceWildcardMatch(patternParts[i], nameParts[i]) {
			return false
		}
	}
	return true
}

func sourceWildcardMatch(pattern, name string) bool {
	patternRunes, nameRunes := []rune(pattern), []rune(name)
	var visit func(int, int) bool
	seen, result := make(map[[2]int]bool), make(map[[2]int]bool)
	visit = func(pi, ni int) bool {
		key := [2]int{pi, ni}
		if seen[key] {
			return result[key]
		}
		seen[key] = true
		matched := false
		switch {
		case pi == len(patternRunes):
			matched = ni == len(nameRunes)
		case patternRunes[pi] == '*':
			matched = visit(pi+1, ni) || ni < len(nameRunes) && visit(pi, ni+1)
		case ni < len(nameRunes) && (patternRunes[pi] == '?' || equalSourceRune(patternRunes[pi], nameRunes[ni])):
			matched = visit(pi+1, ni+1)
		}
		result[key] = matched
		return matched
	}
	return visit(0, 0)
}

func equalSourceRune(a, b rune) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(string(a), string(b))
	}
	return a == b
}

func collectSource(source, archiveAbs string, files map[string]sourceFile) error {
	abs, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return fmt.Errorf("inspect %q: %w", source, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("symbolic-link input %q is not supported", source)
	}
	base := sourceBaseName(source, abs)
	if !info.IsDir() {
		return addSource(abs, base, info, archiveAbs, files)
	}
	return filepath.WalkDir(abs, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entryInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic-link input %q is not supported", name)
		}
		rel, err := filepath.Rel(abs, name)
		if err != nil {
			return err
		}
		archiveName := base
		if rel != "." {
			archiveName = path.Join(base, filepath.ToSlash(rel))
		}
		if archiveName == "." {
			return nil
		}
		return addSource(name, archiveName, entryInfo, archiveAbs, files)
	})
}

func addSource(name, archiveName string, info fs.FileInfo, archiveAbs string, files map[string]sourceFile) error {
	if !info.Mode().IsRegular() && !info.IsDir() {
		return fmt.Errorf("special input %q (%s) is not supported", name, info.Mode().Type())
	}
	abs, err := filepath.Abs(name)
	if err != nil {
		return err
	}
	if sameFilePath(abs, archiveAbs) {
		return nil
	}
	clean, err := safeName(archiveName)
	if err != nil {
		return err
	}
	key := nameKey(clean)
	if previous, exists := files[key]; exists && !sameFilePath(previous.path, abs) {
		return fmt.Errorf("inputs %q and %q have the same archive name %q", previous.path, abs, clean)
	}
	files[key] = sourceFile{path: abs, name: clean, info: info}
	return nil
}

func sourceBaseName(source, abs string) string {
	if filepath.IsAbs(source) {
		return filepath.Base(abs)
	}
	clean := filepath.Clean(source)
	for clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		clean = filepath.Base(clean)
	}
	clean = strings.TrimPrefix(clean, "."+string(filepath.Separator))
	return filepath.ToSlash(clean)
}

func safeName(name string) (string, error) {
	_, relative, err := archive7z.SafeDestination(".", name, false)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(relative), nil
}

func nameKey(name string) string {
	name = path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if runtime.GOOS == "windows" {
		return strings.ToLower(name)
	}
	return name
}

func sameFilePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func ensureSources(sources []sourceFile) error {
	if len(sources) == 0 {
		return errors.New("no files to process")
	}
	return nil
}

func filterSources(sources []sourceFile, excludes []string, recursive bool) ([]sourceFile, error) {
	if len(excludes) == 0 {
		return sources, nil
	}
	patterns := archive7z.BuildPatterns(nil, excludes, recursive)
	filtered := sources[:0]
	for _, source := range sources {
		selected, err := archive7z.Matches(source.name, patterns)
		if err != nil {
			return nil, err
		}
		if selected {
			filtered = append(filtered, source)
		}
	}
	return filtered, nil
}
