package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestTestingDocsListMatchesActualTestFiles(t *testing.T) {
	docBytes, err := os.ReadFile(filepath.Join("docs", "agent", "testing.md"))
	if err != nil {
		t.Fatalf("read testing docs: %v", err)
	}

	docFiles := map[string]bool{}
	re := regexp.MustCompile("^\\| `([^`]+_test\\.go)` \\|")
	for _, line := range strings.Split(string(docBytes), "\n") {
		match := re.FindStringSubmatch(line)
		if len(match) == 2 {
			docFiles[match[1]] = true
		}
	}

	actualMatches, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("glob test files: %v", err)
	}
	actualFiles := map[string]bool{}
	for _, path := range actualMatches {
		actualFiles[filepath.Base(path)] = true
	}

	var missingInDocs []string
	for file := range actualFiles {
		if !docFiles[file] {
			missingInDocs = append(missingInDocs, file)
		}
	}
	var staleInDocs []string
	for file := range docFiles {
		if !actualFiles[file] {
			staleInDocs = append(staleInDocs, file)
		}
	}
	sort.Strings(missingInDocs)
	sort.Strings(staleInDocs)

	if len(missingInDocs) > 0 || len(staleInDocs) > 0 {
		t.Fatalf("testing docs mismatch; missing in docs: %s; stale in docs: %s",
			strings.Join(missingInDocs, ", "),
			strings.Join(staleInDocs, ", "),
		)
	}
}
