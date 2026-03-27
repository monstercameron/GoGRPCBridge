package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetAPIGuardDocumentationPath_SelectsFirstExistingPath(parseT *testing.T) {
	parseRootPath := parseT.TempDir()
	parseDocumentationPath := filepath.Join(parseRootPath, "docs", "core")
	if parseErr := os.MkdirAll(parseDocumentationPath, 0o755); parseErr != nil {
		parseT.Fatalf("os.MkdirAll() error = %v", parseErr)
	}

	parseCompatibilityPath := filepath.Join(parseDocumentationPath, "API_COMPATIBILITY.md")
	if parseErr := os.WriteFile(parseCompatibilityPath, []byte("# compatibility\n"), 0o644); parseErr != nil {
		parseT.Fatalf("os.WriteFile() error = %v", parseErr)
	}

	parseResolvedPath, parseErr := getAPIGuardDocumentationPath(parseRootPath, []string{
		"docs/core/API_COMPATIBILITY.md",
		"API_COMPATIBILITY.md",
	})
	if parseErr != nil {
		parseT.Fatalf("getAPIGuardDocumentationPath() error = %v, want nil", parseErr)
	}
	if parseResolvedPath != parseCompatibilityPath {
		parseT.Fatalf("getAPIGuardDocumentationPath() = %q, want %q", parseResolvedPath, parseCompatibilityPath)
	}
}

func TestGetAPIGuardDocumentationPath_MissingPaths(parseT *testing.T) {
	parseRootPath := parseT.TempDir()
	_, parseErr := getAPIGuardDocumentationPath(parseRootPath, []string{
		"docs/core/API_COMPATIBILITY.md",
		"API_COMPATIBILITY.md",
	})
	if parseErr == nil {
		parseT.Fatal("getAPIGuardDocumentationPath() error = nil, want non-nil")
	}
}
