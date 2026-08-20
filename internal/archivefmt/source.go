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
	"github.com/inpadi/7zip/internal/security"
)

type sourceFile struct {
	path     string
	name     string
	info     fs.FileInfo
	root     *security.Root
	relative string
}

func (s sourceFile) open() (*os.File, error) {
	// Root.Open contains any link resolution; the descriptor identity check
	// prevents a post-enumeration path change from redirecting the input.
	file, err := s.root.Open(s.relative)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(s.info, after) {
		file.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("input %q changed after it was enumerated", s.path)
	}
	return file, nil
}

func collectSources(sources []string, archive string, recursive bool) ([]sourceFile, []*security.Root, error) {
	archiveAbs, err := filepath.Abs(archive)
	if err != nil {
		return nil, nil, err
	}
	files := make(map[string]sourceFile)
	rootCache := make(map[string]*security.Root)
	var roots []*security.Root
	closeError := func(err error) ([]sourceFile, []*security.Root, error) {
		closeSourceRoots(roots)
		return nil, nil, err
	}
	for _, source := range sources {
		matches := []string{source}
		if strings.ContainsAny(source, "*?") {
			matches, err = sourceMatches(source, recursive)
			if err != nil {
				return closeError(fmt.Errorf("invalid input wildcard %q: %w", source, err))
			}
			if len(matches) == 0 {
				return closeError(fmt.Errorf("no input files match %q", source))
			}
		}
		for _, match := range matches {
			if err := collectSource(match, archiveAbs, files, rootCache, &roots); err != nil {
				return closeError(err)
			}
		}
	}
	result := make([]sourceFile, 0, len(files))
	for _, file := range files {
		result = append(result, file)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result, roots, nil
}

func closeSourceRoots(roots []*security.Root) {
	for _, root := range roots {
		_ = root.Close()
	}
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
			if len(matches) >= security.MaxArchiveEntries {
				return fmt.Errorf("input wildcard matches more than %d paths", security.MaxArchiveEntries)
			}
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

func collectSource(source, archiveAbs string, files map[string]sourceFile, rootCache map[string]*security.Root, roots *[]*security.Root) error {
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
		root, err := cachedSourceRoot(filepath.Dir(abs), rootCache, roots)
		if err != nil {
			return fmt.Errorf("open input parent %q: %w", filepath.Dir(abs), err)
		}
		anchored, err := root.Lstat(filepath.Base(abs))
		if err != nil || !os.SameFile(info, anchored) {
			if err != nil {
				return err
			}
			return fmt.Errorf("input %q changed while it was opened", source)
		}
		return addSource(abs, base, anchored, archiveAbs, files, root, filepath.Base(abs))
	}
	root, err := cachedSourceRoot(abs, rootCache, roots)
	if err != nil {
		return fmt.Errorf("open input directory %q: %w", source, err)
	}
	anchored, err := root.Lstat(".")
	if err != nil || !os.SameFile(info, anchored) {
		if err != nil {
			return err
		}
		return fmt.Errorf("input directory %q changed while it was opened", source)
	}
	return fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
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
		archiveName := base
		if name != "." {
			archiveName = path.Join(base, name)
		}
		if archiveName == "." {
			return nil
		}
		display := abs
		if name != "." {
			display = filepath.Join(abs, filepath.FromSlash(name))
		}
		return addSource(display, archiveName, entryInfo, archiveAbs, files, root, name)
	})
}

func cachedSourceRoot(name string, cache map[string]*security.Root, roots *[]*security.Root) (*security.Root, error) {
	key := filepath.Clean(name)
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	if root := cache[key]; root != nil {
		return root, nil
	}
	root, err := security.OpenExistingRoot(name)
	if err != nil {
		return nil, err
	}
	cache[key] = root
	*roots = append(*roots, root)
	return root, nil
}

func addSource(name, archiveName string, info fs.FileInfo, archiveAbs string, files map[string]sourceFile, root *security.Root, relative string) error {
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
	previous, exists := files[key]
	if exists && !sameFilePath(previous.path, abs) {
		return fmt.Errorf("inputs %q and %q have the same archive name %q", previous.path, abs, clean)
	}
	if !exists && len(files) >= security.MaxArchiveEntries {
		return fmt.Errorf("input contains more than %d entries", security.MaxArchiveEntries)
	}
	files[key] = sourceFile{path: abs, name: clean, info: info, root: root, relative: relative}
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
