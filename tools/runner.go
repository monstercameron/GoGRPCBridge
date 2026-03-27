package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type handleRunnerAction func(context.Context, string) error

type storeRunnerCommand struct {
	handleRunnerAction   handleRunnerAction
	getRunnerDescription string
}

const parseRunnerCanonicalModulePath = "github.com/monstercameron/GoGRPCBridge"
const parseRunnerCanonicalRepositoryURL = "https://github.com/monstercameron/GoGRPCBridge"
const parseRunnerCanonicalGoProxyEnvKey = "RUNNER_CANONICAL_GOPROXY"
const parseRunnerCanonicalSkipOriginEnvKey = "RUNNER_CANONICAL_SKIP_ORIGIN"

// main executes the repository task runner.
func main() {
	if handleRunnerMainError := handleRunnerMain(context.Background(), os.Args[1:]); handleRunnerMainError != nil {
		_, _ = fmt.Fprintf(os.Stderr, "runner error: %v\n", handleRunnerMainError)
		os.Exit(1)
	}
}

// handleRunnerMain resolves the command and executes it from repository root.
func handleRunnerMain(parseRunnerContext context.Context, parseRunnerArguments []string) error {
	parseRunnerRootPath, getRunnerRootError := getRunnerRootPath()
	if getRunnerRootError != nil {
		return getRunnerRootError
	}

	storeRunnerCommands := buildRunnerCommands()
	if len(parseRunnerArguments) == 0 || parseRunnerArguments[0] == "help" || parseRunnerArguments[0] == "-h" || parseRunnerArguments[0] == "--help" {
		renderRunnerHelp(storeRunnerCommands)
		return nil
	}

	parseRunnerCommandName := parseRunnerArguments[0]
	getRunnerCommand, hasRunnerCommand := storeRunnerCommands[parseRunnerCommandName]
	if !hasRunnerCommand {
		renderRunnerHelp(storeRunnerCommands)
		return fmt.Errorf("unknown command %q", parseRunnerCommandName)
	}

	return getRunnerCommand.handleRunnerAction(parseRunnerContext, parseRunnerRootPath)
}

// getRunnerRootPath locates the GoGRPCBridge repository root from this source file.
func getRunnerRootPath() (string, error) {
	_, parseRunnerFilePath, _, hasRunnerCaller := runtime.Caller(0)
	if !hasRunnerCaller {
		return "", errors.New("failed to locate runner source path")
	}

	parseRunnerRootPath := filepath.Clean(filepath.Join(filepath.Dir(parseRunnerFilePath), ".."))
	parseGoModPath := filepath.Join(parseRunnerRootPath, "go.mod")
	if _, getRunnerStatError := os.Stat(parseGoModPath); getRunnerStatError != nil {
		return "", fmt.Errorf("failed to validate repository root %q: %w", parseRunnerRootPath, getRunnerStatError)
	}

	return parseRunnerRootPath, nil
}

// buildRunnerCommands defines all supported runner commands.
func buildRunnerCommands() map[string]storeRunnerCommand {
	return map[string]storeRunnerCommand{
		"test": {
			handleRunnerAction:   handleRunnerTest,
			getRunnerDescription: "Run all package tests with race and coverage",
		},
		"test-short": {
			handleRunnerAction:   handleRunnerTestShort,
			getRunnerDescription: "Run package tests without race detector",
		},
		"fmt": {
			handleRunnerAction:   formatRunnerCode,
			getRunnerDescription: "Format Go source with gofmt and goimports",
		},
		"lint": {
			handleRunnerAction:   handleRunnerLint,
			getRunnerDescription: "Run golangci-lint",
		},
		"lint-fix": {
			handleRunnerAction:   applyRunnerLintFix,
			getRunnerDescription: "Run golangci-lint with auto-fix",
		},
		"check": {
			handleRunnerAction:   handleRunnerCheck,
			getRunnerDescription: "Run fmt, lint, and test-short",
		},
		"pre-commit": {
			handleRunnerAction:   handleRunnerPreCommit,
			getRunnerDescription: "Run check and print pre-commit status",
		},
		"install-hooks": {
			handleRunnerAction:   applyRunnerInstallHooks,
			getRunnerDescription: "Mark .git/hooks/pre-commit executable on non-Windows systems",
		},
		"fuzz": {
			handleRunnerAction:   handleRunnerFuzz,
			getRunnerDescription: "Run bridge fuzz tests for 60s each",
		},
		"fuzz-quick": {
			handleRunnerAction:   handleRunnerFuzzQuick,
			getRunnerDescription: "Run bridge fuzz tests for 5s each",
		},
		"e2e": {
			handleRunnerAction:   handleRunnerE2E,
			getRunnerDescription: "Run end-to-end tests",
		},
		"build": {
			handleRunnerAction:   buildRunnerExamples,
			getRunnerDescription: "Build example binaries and WASM example",
		},
		"quality": {
			handleRunnerAction:   handleRunnerQuality,
			getRunnerDescription: "Run enforceable quality gates (lint, tests, coverage, benchmarks)",
		},
		"quality-baseline": {
			handleRunnerAction:   storeRunnerQualityBaseline,
			getRunnerDescription: "Capture benchmark baseline snapshot for quality gates",
		},
		"quality-trend": {
			handleRunnerAction:   handleRunnerQualityTrend,
			getRunnerDescription: "Compare benchmark output against quality baseline and store trend summary",
		},
		"canonical-publish-check": {
			handleRunnerAction:   handleRunnerCanonicalPublishCheck,
			getRunnerDescription: "Verify canonical module/repository identity and clean-consumer go-get smoke",
		},
		"clean": {
			handleRunnerAction:   clearRunnerArtifacts,
			getRunnerDescription: "Clean caches and build artifacts",
		},
	}
}

// renderRunnerHelp prints supported command names and descriptions.
func renderRunnerHelp(storeRunnerCommands map[string]storeRunnerCommand) {
	_, _ = fmt.Fprintln(os.Stdout, "Usage: go run ./tools/runner.go <command>")
	_, _ = fmt.Fprintln(os.Stdout, "")
	_, _ = fmt.Fprintln(os.Stdout, "Available commands:")

	storeRunnerCommandNames := make([]string, 0, len(storeRunnerCommands))
	for parseRunnerCommandName := range storeRunnerCommands {
		storeRunnerCommandNames = append(storeRunnerCommandNames, parseRunnerCommandName)
	}
	sort.Strings(storeRunnerCommandNames)

	for _, parseRunnerCommandName := range storeRunnerCommandNames {
		getRunnerCommand := storeRunnerCommands[parseRunnerCommandName]
		_, _ = fmt.Fprintf(os.Stdout, "  %-15s %s\n", parseRunnerCommandName, getRunnerCommand.getRunnerDescription)
	}
}

// handleRunnerTest runs package tests with race detection and coverage output.
func handleRunnerTest(parseRunnerContext context.Context, parseRunnerRootPath string) error {
	return handleRunnerProcess(parseRunnerContext, parseRunnerRootPath, parseRunnerRootPath, nil, "go", "test", "./pkg/...", "-v", "-race", "-coverprofile=coverage.txt")
}

// handleRunnerTestShort runs package tests without race detection.
func handleRunnerTestShort(parseRunnerContext context.Context, parseRunnerRootPath string) error {
	return handleRunnerProcess(parseRunnerContext, parseRunnerRootPath, parseRunnerRootPath, nil, "go", "test", "./pkg/...", "-short")
}

// formatRunnerCode formats repository Go files with gofmt and goimports.
func formatRunnerCode(parseRunnerContext context.Context, parseRunnerRootPath string) error {
	if getRunnerBinaryLookupError := getRunnerBinaryError("goimports"); getRunnerBinaryLookupError != nil {
		return getRunnerBinaryLookupError
	}
	if handleRunnerProcessError := handleRunnerProcess(parseRunnerContext, parseRunnerRootPath, parseRunnerRootPath, nil, "gofmt", "-w", "-s", "."); handleRunnerProcessError != nil {
		return handleRunnerProcessError
	}
	return handleRunnerProcess(parseRunnerContext, parseRunnerRootPath, parseRunnerRootPath, nil, "goimports", "-w", ".")
}

// handleRunnerLint runs golangci-lint with repository configuration.
func handleRunnerLint(parseRunnerContext context.Context, parseRunnerRootPath string) error {
	if getRunnerBinaryLookupError := getRunnerBinaryError("golangci-lint"); getRunnerBinaryLookupError != nil {
		return getRunnerBinaryLookupError
	}
	return handleRunnerProcess(parseRunnerContext, parseRunnerRootPath, parseRunnerRootPath, nil, "golangci-lint", "run", "--config=.golangci.yml", "./...")
}

// applyRunnerLintFix runs golangci-lint in auto-fix mode.
func applyRunnerLintFix(parseRunnerContext context.Context, parseRunnerRootPath string) error {
	if getRunnerBinaryLookupError := getRunnerBinaryError("golangci-lint"); getRunnerBinaryLookupError != nil {
		return getRunnerBinaryLookupError
	}
	return handleRunnerProcess(parseRunnerContext, parseRunnerRootPath, parseRunnerRootPath, nil, "golangci-lint", "run", "--config=.golangci.yml", "--fix", "./...")
}

// handleRunnerCheck runs fmt, lint, and test-short in sequence.
func handleRunnerCheck(parseRunnerContext context.Context, parseRunnerRootPath string) error {
	if formatRunnerCodeError := formatRunnerCode(parseRunnerContext, parseRunnerRootPath); formatRunnerCodeError != nil {
		return formatRunnerCodeError
	}
	if handleRunnerLintError := handleRunnerLint(parseRunnerContext, parseRunnerRootPath); handleRunnerLintError != nil {
		return handleRunnerLintError
	}
	return handleRunnerTestShort(parseRunnerContext, parseRunnerRootPath)
}

// handleRunnerPreCommit runs pre-commit validation checks.
func handleRunnerPreCommit(parseRunnerContext context.Context, parseRunnerRootPath string) error {
	if handleRunnerCheckError := handleRunnerCheck(parseRunnerContext, parseRunnerRootPath); handleRunnerCheckError != nil {
		return handleRunnerCheckError
	}
	_, _ = fmt.Fprintln(os.Stdout, "Pre-commit checks passed.")
	return nil
}

// handleRunnerCanonicalPublishCheck validates canonical module identity and clean-consumer go-get behavior.
func handleRunnerCanonicalPublishCheck(parseRunnerContext context.Context, parseRunnerRootPath string) error {
	parseGoModPath := filepath.Join(parseRunnerRootPath, "go.mod")
	parseGoModBytes, readRunnerGoModError := os.ReadFile(parseGoModPath)
	if readRunnerGoModError != nil {
		return fmt.Errorf("failed to read %q: %w", parseGoModPath, readRunnerGoModError)
	}

	parseModulePath, parseModulePathError := parseRunnerGoModModulePath(string(parseGoModBytes))
	if parseModulePathError != nil {
		return parseModulePathError
	}
	if parseModulePath != parseRunnerCanonicalModulePath {
		return fmt.Errorf(
			"canonical publish check failed: go.mod module path %q does not match canonical %q",
			parseModulePath,
			parseRunnerCanonicalModulePath,
		)
	}

	storeRunnerCanonicalRepositoryURLs := buildRunnerCanonicalRepositoryURLs()
	parseCanonicalRepositoryURL := storeRunnerCanonicalRepositoryURLs[0]
	if !hasRunnerSkipCanonicalOriginCheck() {
		parseOriginURLOutput, parseOriginURLError := buildRunnerProcessOutput(parseRunnerContext, parseRunnerRootPath, parseRunnerRootPath, nil, "git", "remote", "get-url", "origin")
		if parseOriginURLError != nil {
			return parseOriginURLError
		}
		parseOriginURL := strings.TrimSpace(parseOriginURLOutput)
		parseOriginURL = normalizeRunnerRepositoryURL(parseOriginURL)
		if !hasRunnerCanonicalRepositoryURL(parseOriginURL, storeRunnerCanonicalRepositoryURLs) {
			return fmt.Errorf(
				"canonical publish check failed: origin remote %q does not match accepted canonical repositories %q",
				parseOriginURL,
				strings.Join(storeRunnerCanonicalRepositoryURLs, ", "),
			)
		}
	} else {
		_, _ = fmt.Fprintln(os.Stdout, "canonical publish check: skipping origin remote validation")
	}

	for _, parseRepositoryURL := range storeRunnerCanonicalRepositoryURLs {
		if _, parseRepositoryLookupError := buildRunnerProcessOutput(parseRunnerContext, parseRunnerRootPath, parseRunnerRootPath, nil, "git", "ls-remote", parseRepositoryURL, "HEAD"); parseRepositoryLookupError != nil {
			return fmt.Errorf("canonical publish check failed: repository %q is not reachable: %w", parseRepositoryURL, parseRepositoryLookupError)
		}
	}

	parseSmokeDirPath, createRunnerSmokeDirError := os.MkdirTemp("", "gogrpcbridge-publish-smoke-*")
	if createRunnerSmokeDirError != nil {
		return fmt.Errorf("failed to create temporary smoke directory: %w", createRunnerSmokeDirError)
	}
	defer func() {
		_ = os.RemoveAll(parseSmokeDirPath)
	}()

	storeRunnerCanonicalEnv := buildRunnerCanonicalEnv()
	if handleRunnerProcessError := handleRunnerProcess(parseRunnerContext, parseRunnerRootPath, parseSmokeDirPath, storeRunnerCanonicalEnv, "go", "mod", "init", "example.com/gogrpcbridge-publish-smoke"); handleRunnerProcessError != nil {
		return handleRunnerProcessError
	}
	if handleRunnerProcessError := handleRunnerProcess(parseRunnerContext, parseRunnerRootPath, parseSmokeDirPath, storeRunnerCanonicalEnv, "go", "get", parseRunnerCanonicalModulePath+"@latest"); handleRunnerProcessError != nil {
		return fmt.Errorf(
			"canonical publish check failed: clean-consumer go get for %q failed. publish a new semver tag from %q with matching module path %q: %w",
			parseRunnerCanonicalModulePath,
			parseCanonicalRepositoryURL,
			parseRunnerCanonicalModulePath,
			handleRunnerProcessError,
		)
	}
	if handleRunnerProcessError := handleRunnerProcess(parseRunnerContext, parseRunnerRootPath, parseSmokeDirPath, storeRunnerCanonicalEnv, "go", "get", parseRunnerCanonicalModulePath+"/pkg/grpctunnel@latest"); handleRunnerProcessError != nil {
		return fmt.Errorf(
			"canonical publish check failed: clean-consumer package go get for %q failed: %w",
			parseRunnerCanonicalModulePath+"/pkg/grpctunnel@latest",
			handleRunnerProcessError,
		)
	}

	parseServerDirPath := filepath.Join(parseSmokeDirPath, "server")
	if createRunnerServerDirectoryError := os.MkdirAll(parseServerDirPath, 0o755); createRunnerServerDirectoryError != nil {
		return fmt.Errorf("failed to create smoke server directory %q: %w", parseServerDirPath, createRunnerServerDirectoryError)
	}
	parseSmokeBinPath := filepath.Join(parseSmokeDirPath, "bin")
	if createRunnerSmokeBinDirectoryError := os.MkdirAll(parseSmokeBinPath, 0o755); createRunnerSmokeBinDirectoryError != nil {
		return fmt.Errorf("failed to create smoke bin directory %q: %w", parseSmokeBinPath, createRunnerSmokeBinDirectoryError)
	}
	parseServerMainPath := filepath.Join(parseServerDirPath, "main.go")
	parseServerMainContent := "package main\n\nimport _ \"" + parseRunnerCanonicalModulePath + "/pkg/grpctunnel\"\n\nfunc main() {}\n"
	if storeRunnerServerMainError := os.WriteFile(parseServerMainPath, []byte(parseServerMainContent), 0o644); storeRunnerServerMainError != nil {
		return fmt.Errorf("failed to write smoke server main file %q: %w", parseServerMainPath, storeRunnerServerMainError)
	}
	if handleRunnerProcessError := handleRunnerProcess(parseRunnerContext, parseRunnerRootPath, parseSmokeDirPath, storeRunnerCanonicalEnv, "go", "build", "-o", filepath.Join("bin", "server-smoke"), "./server"); handleRunnerProcessError != nil {
		return fmt.Errorf("canonical publish check failed: clean-consumer server compile smoke failed: %w", handleRunnerProcessError)
	}

	parseWASMDirPath := filepath.Join(parseSmokeDirPath, "wasm")
	if createRunnerWASMDirectoryError := os.MkdirAll(parseWASMDirPath, 0o755); createRunnerWASMDirectoryError != nil {
		return fmt.Errorf("failed to create smoke wasm directory %q: %w", parseWASMDirPath, createRunnerWASMDirectoryError)
	}
	parseWASMMainPath := filepath.Join(parseWASMDirPath, "main.go")
	parseWASMMainContent := "//go:build js && wasm\n\npackage main\n\nimport _ \"" + parseRunnerCanonicalModulePath + "/pkg/grpctunnel\"\n\nfunc main() {}\n"
	if storeRunnerWASMMainError := os.WriteFile(parseWASMMainPath, []byte(parseWASMMainContent), 0o644); storeRunnerWASMMainError != nil {
		return fmt.Errorf("failed to write smoke wasm main file %q: %w", parseWASMMainPath, storeRunnerWASMMainError)
	}
	storeRunnerWASMEnv := buildRunnerCanonicalWASMEnv(storeRunnerCanonicalEnv)
	if handleRunnerProcessError := handleRunnerProcess(parseRunnerContext, parseRunnerRootPath, parseSmokeDirPath, storeRunnerWASMEnv, "go", "build", "-o", filepath.Join("bin", "wasm-smoke.wasm"), "./wasm"); handleRunnerProcessError != nil {
		return fmt.Errorf("canonical publish check failed: clean-consumer wasm compile smoke failed: %w", handleRunnerProcessError)
	}

	_, _ = fmt.Fprintln(os.Stdout, "canonical publish check passed")
	return nil
}

// buildRunnerCanonicalEnv builds optional environment overrides for canonical publish smoke commands.
func buildRunnerCanonicalEnv() map[string]string {
	parseRunnerCanonicalGoProxyValue := strings.TrimSpace(os.Getenv(parseRunnerCanonicalGoProxyEnvKey))
	if parseRunnerCanonicalGoProxyValue == "" {
		return nil
	}

	return map[string]string{
		"GOPROXY": parseRunnerCanonicalGoProxyValue,
	}
}

// buildRunnerCanonicalWASMEnv returns environment variables for wasm smoke builds.
func buildRunnerCanonicalWASMEnv(storeRunnerCanonicalEnv map[string]string) map[string]string {
	storeRunnerWASMEnv := map[string]string{
		"GOOS":   "js",
		"GOARCH": "wasm",
	}
	for parseKey, parseValue := range storeRunnerCanonicalEnv {
		storeRunnerWASMEnv[parseKey] = parseValue
	}
	return storeRunnerWASMEnv
}

// buildRunnerCanonicalRepositoryURLs returns accepted repository URLs for identity checks.
func buildRunnerCanonicalRepositoryURLs() []string {
	parseRepositoryURL := normalizeRunnerRepositoryURL(parseRunnerCanonicalRepositoryURL)
	if parseRepositoryURL == "" {
		return nil
	}
	return []string{parseRepositoryURL}
}

// hasRunnerCanonicalRepositoryURL reports whether a repository URL is accepted for canonical checks.
func hasRunnerCanonicalRepositoryURL(parseRepositoryURL string, storeRunnerCanonicalRepositoryURLs []string) bool {
	parseRepositoryURL = normalizeRunnerRepositoryURL(parseRepositoryURL)
	for _, parseAllowedURL := range storeRunnerCanonicalRepositoryURLs {
		if parseRepositoryURL == parseAllowedURL {
			return true
		}
	}
	return false
}

// hasRunnerSkipCanonicalOriginCheck reports whether origin remote validation should be skipped.
func hasRunnerSkipCanonicalOriginCheck() bool {
	parseSkipOriginValue := strings.TrimSpace(strings.ToLower(os.Getenv(parseRunnerCanonicalSkipOriginEnvKey)))
	return parseSkipOriginValue == "1" || parseSkipOriginValue == "true" || parseSkipOriginValue == "yes"
}

// parseRunnerGoModModulePath parses go.mod content and returns the declared module path.
func parseRunnerGoModModulePath(parseGoModContent string) (string, error) {
	parseModulePattern := regexp.MustCompile(`(?m)^module\s+(\S+)\s*$`)
	parseModuleMatch := parseModulePattern.FindStringSubmatch(parseGoModContent)
	if len(parseModuleMatch) != 2 {
		return "", fmt.Errorf("failed to parse module path from go.mod")
	}
	return strings.TrimSpace(parseModuleMatch[1]), nil
}

// normalizeRunnerRepositoryURL normalizes Git repository URLs across HTTPS and SSH forms.
func normalizeRunnerRepositoryURL(parseRepositoryURL string) string {
	parseRepositoryURL = strings.TrimSpace(parseRepositoryURL)
	parseRepositoryURL = strings.TrimSuffix(parseRepositoryURL, ".git")

	parseSSHPrefix := "git@github.com:"
	if strings.HasPrefix(parseRepositoryURL, parseSSHPrefix) {
		parseRepositoryURL = "https://github.com/" + strings.TrimPrefix(parseRepositoryURL, parseSSHPrefix)
	}
	return parseRepositoryURL
}

// applyRunnerInstallHooks ensures the pre-commit hook is executable where supported.
func applyRunnerInstallHooks(parseRunnerContext context.Context, parseRunnerRootPath string) error {
	_ = parseRunnerContext

	parseRunnerHookPath := filepath.Join(parseRunnerRootPath, ".git", "hooks", "pre-commit")
	if _, getRunnerHookStatError := os.Stat(parseRunnerHookPath); getRunnerHookStatError != nil {
		return fmt.Errorf("failed to locate git hook %q: %w", parseRunnerHookPath, getRunnerHookStatError)
	}

	isRunnerWindows := runtime.GOOS == "windows"
	if isRunnerWindows {
		_, _ = fmt.Fprintln(os.Stdout, "Hook file exists. Skipping chmod on Windows.")
		return nil
	}

	if applyRunnerHookModeError := os.Chmod(parseRunnerHookPath, 0o755); applyRunnerHookModeError != nil {
		return fmt.Errorf("failed to set executable bit on %q: %w", parseRunnerHookPath, applyRunnerHookModeError)
	}

	_, _ = fmt.Fprintln(os.Stdout, "Pre-commit hook is executable.")
	return nil
}

// handleRunnerFuzz runs bridge fuzz tests for 60 seconds each.
func handleRunnerFuzz(parseRunnerContext context.Context, parseRunnerRootPath string) error {
	if handleRunnerSingleFuzzError := handleRunnerSingleFuzz(parseRunnerContext, parseRunnerRootPath, "FuzzWebSocketConnWrite", "60s"); handleRunnerSingleFuzzError != nil {
		return handleRunnerSingleFuzzError
	}
	if handleRunnerSingleFuzzError := handleRunnerSingleFuzz(parseRunnerContext, parseRunnerRootPath, "FuzzWebSocketConnRead", "60s"); handleRunnerSingleFuzzError != nil {
		return handleRunnerSingleFuzzError
	}
	if handleRunnerSingleFuzzError := handleRunnerSingleFuzz(parseRunnerContext, parseRunnerRootPath, "FuzzBinaryMessage", "60s"); handleRunnerSingleFuzzError != nil {
		return handleRunnerSingleFuzzError
	}
	return handleRunnerSingleFuzz(parseRunnerContext, parseRunnerRootPath, "FuzzMessageSizes", "60s")
}

// handleRunnerFuzzQuick runs bridge fuzz tests for 5 seconds each.
func handleRunnerFuzzQuick(parseRunnerContext context.Context, parseRunnerRootPath string) error {
	if handleRunnerSingleFuzzError := handleRunnerSingleFuzz(parseRunnerContext, parseRunnerRootPath, "FuzzWebSocketConnWrite", "5s"); handleRunnerSingleFuzzError != nil {
		return handleRunnerSingleFuzzError
	}
	if handleRunnerSingleFuzzError := handleRunnerSingleFuzz(parseRunnerContext, parseRunnerRootPath, "FuzzWebSocketConnRead", "5s"); handleRunnerSingleFuzzError != nil {
		return handleRunnerSingleFuzzError
	}
	if handleRunnerSingleFuzzError := handleRunnerSingleFuzz(parseRunnerContext, parseRunnerRootPath, "FuzzBinaryMessage", "5s"); handleRunnerSingleFuzzError != nil {
		return handleRunnerSingleFuzzError
	}
	return handleRunnerSingleFuzz(parseRunnerContext, parseRunnerRootPath, "FuzzMessageSizes", "5s")
}

// handleRunnerSingleFuzz executes one fuzz target in the bridge package.
func handleRunnerSingleFuzz(parseRunnerContext context.Context, parseRunnerRootPath string, parseRunnerFuzzName string, parseRunnerFuzzDuration string) error {
	_, _ = fmt.Fprintf(os.Stdout, "Running %s (%s)\n", parseRunnerFuzzName, parseRunnerFuzzDuration)
	return handleRunnerProcess(parseRunnerContext, parseRunnerRootPath, parseRunnerRootPath, nil, "go", "test", "./pkg/bridge", "-v", "-fuzz="+parseRunnerFuzzName, "-fuzztime="+parseRunnerFuzzDuration)
}

// handleRunnerE2E runs repository end-to-end tests.
func handleRunnerE2E(parseRunnerContext context.Context, parseRunnerRootPath string) error {
	return handleRunnerProcess(parseRunnerContext, parseRunnerRootPath, parseRunnerRootPath, nil, "go", "test", "./e2e/...", "-v", "-timeout", "5m")
}

// buildRunnerExamples compiles example binaries and the WASM client example.
func buildRunnerExamples(parseRunnerContext context.Context, parseRunnerRootPath string) error {
	if handleRunnerProcessError := handleRunnerProcess(parseRunnerContext, parseRunnerRootPath, parseRunnerRootPath, nil, "go", "build", "-v", "./examples/direct-bridge"); handleRunnerProcessError != nil {
		return handleRunnerProcessError
	}
	if handleRunnerProcessError := handleRunnerProcess(parseRunnerContext, parseRunnerRootPath, parseRunnerRootPath, nil, "go", "build", "-v", "./examples/grpc-server"); handleRunnerProcessError != nil {
		return handleRunnerProcessError
	}

	storeRunnerEnv := map[string]string{
		"GOOS":   "js",
		"GOARCH": "wasm",
	}
	parseRunnerWASMPath := filepath.Join(parseRunnerRootPath, "examples", "wasm-client")
	return handleRunnerProcess(parseRunnerContext, parseRunnerRootPath, parseRunnerWASMPath, storeRunnerEnv, "go", "build", "-v", ".")
}

// clearRunnerArtifacts removes build artifacts and Go caches.
func clearRunnerArtifacts(parseRunnerContext context.Context, parseRunnerRootPath string) error {
	if handleRunnerProcessError := handleRunnerProcess(parseRunnerContext, parseRunnerRootPath, parseRunnerRootPath, nil, "go", "clean", "-cache", "-testcache", "-modcache"); handleRunnerProcessError != nil {
		return handleRunnerProcessError
	}

	storeRunnerArtifactPaths := []string{
		filepath.Join(parseRunnerRootPath, "coverage.txt"),
		filepath.Join(parseRunnerRootPath, "direct-bridge"),
		filepath.Join(parseRunnerRootPath, "direct-bridge.exe"),
		filepath.Join(parseRunnerRootPath, "grpc-server"),
		filepath.Join(parseRunnerRootPath, "grpc-server.exe"),
		filepath.Join(parseRunnerRootPath, "examples", "wasm-client", "main.wasm"),
	}

	for _, parseRunnerArtifactPath := range storeRunnerArtifactPaths {
		if clearRunnerFileError := clearRunnerFile(parseRunnerArtifactPath); clearRunnerFileError != nil {
			return clearRunnerFileError
		}
	}
	return nil
}

// clearRunnerFile deletes one file path and ignores missing files.
func clearRunnerFile(parseRunnerFilePath string) error {
	if clearRunnerRemoveError := os.Remove(parseRunnerFilePath); clearRunnerRemoveError != nil && !errors.Is(clearRunnerRemoveError, os.ErrNotExist) {
		return fmt.Errorf("failed to remove %q: %w", parseRunnerFilePath, clearRunnerRemoveError)
	}
	return nil
}

// getRunnerBinaryError checks whether an external binary exists in PATH.
func getRunnerBinaryError(parseRunnerBinaryName string) error {
	if _, getRunnerLookupError := exec.LookPath(parseRunnerBinaryName); getRunnerLookupError != nil {
		return fmt.Errorf("required binary %q was not found in PATH", parseRunnerBinaryName)
	}
	return nil
}

// handleRunnerProcess executes one process with inherited stdin/stdout/stderr.
func handleRunnerProcess(parseRunnerContext context.Context, parseRunnerRootPath string, parseRunnerWorkPath string, storeRunnerEnv map[string]string, parseRunnerBinaryName string, parseRunnerArgs ...string) error {
	parseRunnerCommand := exec.CommandContext(parseRunnerContext, parseRunnerBinaryName, parseRunnerArgs...)
	parseRunnerCommand.Stdin = os.Stdin
	parseRunnerCommand.Stdout = os.Stdout
	parseRunnerCommand.Stderr = os.Stderr
	parseRunnerCommand.Dir = parseRunnerWorkPath
	parseRunnerCommand.Env = buildRunnerEnv(parseRunnerRootPath, storeRunnerEnv)

	if handleRunnerStartError := parseRunnerCommand.Run(); handleRunnerStartError != nil {
		return fmt.Errorf("failed to execute %s %v: %w", parseRunnerBinaryName, parseRunnerArgs, handleRunnerStartError)
	}
	return nil
}

// buildRunnerEnv builds process environment with optional overrides.
func buildRunnerEnv(parseRunnerRootPath string, storeRunnerEnv map[string]string) []string {
	storeRunnerProcessEnv := os.Environ()
	storeRunnerProcessEnv = append(storeRunnerProcessEnv, "GOWORK=off")
	storeRunnerProcessEnv = append(storeRunnerProcessEnv, "RUNNER_ROOT="+parseRunnerRootPath)
	for parseRunnerEnvName, parseRunnerEnvValue := range storeRunnerEnv {
		storeRunnerProcessEnv = append(storeRunnerProcessEnv, parseRunnerEnvName+"="+parseRunnerEnvValue)
	}
	return storeRunnerProcessEnv
}

type storeRunnerBenchmarkRecord struct {
	GetBenchmarkName   string  `json:"benchmark_name"`
	GetNsPerOp         float64 `json:"ns_per_op"`
	GetBytesPerOp      float64 `json:"bytes_per_op"`
	GetAllocsPerOp     float64 `json:"allocs_per_op"`
	GetKilobytesPerOp  float64 `json:"kilobytes_per_op"`
	HasKilobytesMetric bool    `json:"has_kilobytes_metric"`
}

type storeRunnerBenchmarkSnapshot struct {
	GetGeneratedAtUTC string                                `json:"generated_at_utc"`
	GetGoOS           string                                `json:"go_os"`
	GetGoArch         string                                `json:"go_arch"`
	GetGoVersion      string                                `json:"go_version"`
	StoreBenchmarks   map[string]storeRunnerBenchmarkRecord `json:"benchmarks"`
}

type storeRunnerBenchmarkGateResult struct {
	GetPayloadSavingsPercent     float64 `json:"payload_savings_percent"`
	GetBidiMemorySavingsPercent  float64 `json:"bidi_memory_savings_percent"`
	GetBidiAllocsSavingsPercent  float64 `json:"bidi_allocs_savings_percent"`
	GetLargeMemorySavingsPercent float64 `json:"large_dataset_memory_savings_percent"`
}

type storeRunnerQualitySummaryPayload struct {
	GetGeneratedAtUTC      string                         `json:"generated_at_utc"`
	GetCoveragePercent     float64                        `json:"coverage_percent"`
	GetCoverageThreshold   float64                        `json:"coverage_threshold_percent"`
	GetBenchmarkGateResult storeRunnerBenchmarkGateResult `json:"benchmark_gate_result"`
}

type storeRunnerBenchmarkTrendRecord struct {
	GetBenchmarkName              string  `json:"benchmark_name"`
	GetNsPerOpDeltaPercent        float64 `json:"ns_per_op_delta_percent"`
	GetBytesPerOpDeltaPercent     float64 `json:"bytes_per_op_delta_percent"`
	GetAllocsPerOpDeltaPercent    float64 `json:"allocs_per_op_delta_percent"`
	GetKilobytesPerOpDeltaPercent float64 `json:"kilobytes_per_op_delta_percent"`
}

type storeRunnerBenchmarkTrendSummary struct {
	GetGeneratedAtUTC         string                                     `json:"generated_at_utc"`
	GetBaselineGeneratedAtUTC string                                     `json:"baseline_generated_at_utc"`
	GetBaselineGoVersion      string                                     `json:"baseline_go_version"`
	GetCurrentGoVersion       string                                     `json:"current_go_version"`
	StoreBenchmarks           map[string]storeRunnerBenchmarkTrendRecord `json:"benchmarks"`
}

const (
	getRunnerBenchmarkPayloadSavingsThresholdPercent    = 15.0
	getRunnerBenchmarkBidiMemorySavingsThresholdPercent = 40.0
	getRunnerBenchmarkBidiAllocsSavingsThresholdPercent = 30.0
	// Keep this threshold conservative to avoid false negatives from CI benchmark noise.
	getRunnerBenchmarkLargeMemoryThresholdPercent = 1.0
	getRunnerBenchmarkGateAttempts                = 2
	getRunnerBenchmarkGateRetryDelay              = 2 * time.Second
)

// handleRunnerQuality runs enforceable quality gates for CI and local validation.
func handleRunnerQuality(parseRunnerContext context.Context, parseRunnerRootPath string) error {
	if handleRunnerLintError := handleRunnerLint(parseRunnerContext, parseRunnerRootPath); handleRunnerLintError != nil {
		return handleRunnerLintError
	}

	if handleRunnerQualityTestError := handleRunnerQualityTest(parseRunnerContext, parseRunnerRootPath); handleRunnerQualityTestError != nil {
		return handleRunnerQualityTestError
	}

	parseCoveragePercent, buildRunnerCoveragePercentError := buildRunnerCoveragePercent(parseRunnerContext, parseRunnerRootPath)
	if buildRunnerCoveragePercentError != nil {
		return buildRunnerCoveragePercentError
	}
	if parseCoveragePercent < 90.0 {
		return fmt.Errorf("coverage gate failed: got %.2f%%, need >= 90.00%%", parseCoveragePercent)
	}
	_, _ = fmt.Fprintf(os.Stdout, "coverage gate passed: %.2f%% >= 90.00%%\n", parseCoveragePercent)

	if handleRunnerProcessError := handleRunnerProcess(parseRunnerContext, parseRunnerRootPath, parseRunnerRootPath, nil, "go", "test", "./...", "-run", "^$"); handleRunnerProcessError != nil {
		return handleRunnerProcessError
	}

	parseBenchmarkGateResult, buildRunnerBenchmarkGateResultError := buildRunnerBenchmarkGateResultWithRetry(parseRunnerContext, parseRunnerRootPath)
	if buildRunnerBenchmarkGateResultError != nil {
		return buildRunnerBenchmarkGateResultError
	}

	if storeRunnerQualitySummaryError := storeRunnerQualitySummary(parseRunnerRootPath, parseCoveragePercent, parseBenchmarkGateResult); storeRunnerQualitySummaryError != nil {
		return storeRunnerQualitySummaryError
	}
	return nil
}

// buildRunnerBenchmarkGateResultWithRetry runs benchmark quality gates and retries once to reduce transient CI benchmark noise.
func buildRunnerBenchmarkGateResultWithRetry(parseRunnerContext context.Context, parseRunnerRootPath string) (storeRunnerBenchmarkGateResult, error) {
	var parseLastError error
	for parseAttempt := 1; parseAttempt <= getRunnerBenchmarkGateAttempts; parseAttempt++ {
		parseBenchmarkOutput, buildRunnerBenchmarkOutputError := buildRunnerBenchmarkOutput(parseRunnerContext, parseRunnerRootPath)
		if buildRunnerBenchmarkOutputError != nil {
			parseLastError = buildRunnerBenchmarkOutputError
		} else {
			storeRunnerBenchmarkRecords, parseRunnerBenchmarkRecordsError := parseRunnerBenchmarkRecords(parseBenchmarkOutput)
			if parseRunnerBenchmarkRecordsError != nil {
				parseLastError = parseRunnerBenchmarkRecordsError
			} else {
				parseBenchmarkGateResult, buildRunnerBenchmarkGateResultError := buildRunnerBenchmarkGateResult(storeRunnerBenchmarkRecords)
				if buildRunnerBenchmarkGateResultError == nil {
					return parseBenchmarkGateResult, nil
				}
				parseLastError = buildRunnerBenchmarkGateResultError
			}
		}

		if parseAttempt == getRunnerBenchmarkGateAttempts {
			break
		}

		_, _ = fmt.Fprintf(os.Stdout, "benchmark gate attempt %d/%d failed: %v\n", parseAttempt, getRunnerBenchmarkGateAttempts, parseLastError)
		select {
		case <-time.After(getRunnerBenchmarkGateRetryDelay):
		case <-parseRunnerContext.Done():
			return storeRunnerBenchmarkGateResult{}, parseRunnerContext.Err()
		}
	}
	return storeRunnerBenchmarkGateResult{}, parseLastError
}

// handleRunnerQualityTest runs coverage tests with race when supported by the local toolchain.
func handleRunnerQualityTest(parseRunnerContext context.Context, parseRunnerRootPath string) error {
	if os.Getenv("RUNNER_QUALITY_SKIP_RACE") == "1" {
		_, _ = fmt.Fprintln(os.Stdout, "race gate skipped by RUNNER_QUALITY_SKIP_RACE=1. running non-race coverage test.")
		if handleRunnerProcessError := handleRunnerProcess(parseRunnerContext, parseRunnerRootPath, parseRunnerRootPath, nil, "go", "test", "./pkg/...", "-coverprofile=coverage.txt", "-covermode=atomic"); handleRunnerProcessError != nil {
			return handleRunnerProcessError
		}
		return nil
	}

	parseRaceOutput, buildRunnerRaceOutputError := buildRunnerProcessOutput(
		parseRunnerContext,
		parseRunnerRootPath,
		parseRunnerRootPath,
		map[string]string{"CGO_ENABLED": "1"},
		"go",
		"test",
		"./pkg/...",
		"-race",
		"-coverprofile=coverage.txt",
		"-covermode=atomic",
	)
	if buildRunnerRaceOutputError == nil {
		_, _ = fmt.Fprint(os.Stdout, parseRaceOutput)
		return nil
	}

	if !isRunnerRaceToolchainIssue(parseRaceOutput) {
		return buildRunnerRaceOutputError
	}
	if os.Getenv("RUNNER_STRICT_RACE") == "1" {
		return fmt.Errorf("race gate failed in strict mode: %v", buildRunnerRaceOutputError)
	}

	_, _ = fmt.Fprintln(os.Stdout, "race gate skipped: local toolchain does not support -race with CGO. running non-race coverage test.")
	if handleRunnerProcessError := handleRunnerProcess(parseRunnerContext, parseRunnerRootPath, parseRunnerRootPath, nil, "go", "test", "./pkg/...", "-coverprofile=coverage.txt", "-covermode=atomic"); handleRunnerProcessError != nil {
		return handleRunnerProcessError
	}
	return nil
}

// storeRunnerQualityBaseline captures benchmark metrics into a JSON snapshot file.
func storeRunnerQualityBaseline(parseRunnerContext context.Context, parseRunnerRootPath string) error {
	parseBenchmarkOutput, buildRunnerBenchmarkOutputError := buildRunnerBenchmarkOutput(parseRunnerContext, parseRunnerRootPath)
	if buildRunnerBenchmarkOutputError != nil {
		return buildRunnerBenchmarkOutputError
	}

	storeRunnerBenchmarkRecords, parseRunnerBenchmarkRecordsError := parseRunnerBenchmarkRecords(parseBenchmarkOutput)
	if parseRunnerBenchmarkRecordsError != nil {
		return parseRunnerBenchmarkRecordsError
	}

	parseGoVersion, getRunnerGoVersionError := getRunnerGoVersion(parseRunnerContext, parseRunnerRootPath)
	if getRunnerGoVersionError != nil {
		return getRunnerGoVersionError
	}

	parseSnapshot := storeRunnerBenchmarkSnapshot{
		GetGeneratedAtUTC: time.Now().UTC().Format(time.RFC3339),
		GetGoOS:           runtime.GOOS,
		GetGoArch:         runtime.GOARCH,
		GetGoVersion:      parseGoVersion,
		StoreBenchmarks:   storeRunnerBenchmarkRecords,
	}

	parseSnapshotBytes, parseSnapshotMarshalError := json.MarshalIndent(parseSnapshot, "", "  ")
	if parseSnapshotMarshalError != nil {
		return fmt.Errorf("failed to marshal quality baseline: %w", parseSnapshotMarshalError)
	}

	parseSnapshotPath := filepath.Join(parseRunnerRootPath, "benchmarks", "quality_baseline.json")
	if storeRunnerSnapshotError := os.WriteFile(parseSnapshotPath, parseSnapshotBytes, 0o644); storeRunnerSnapshotError != nil {
		return fmt.Errorf("failed to write %q: %w", parseSnapshotPath, storeRunnerSnapshotError)
	}

	_, _ = fmt.Fprintf(os.Stdout, "stored quality baseline: %s\n", parseSnapshotPath)
	return nil
}

// handleRunnerQualityTrend compares the latest benchmark run against the stored baseline.
func handleRunnerQualityTrend(parseRunnerContext context.Context, parseRunnerRootPath string) error {
	parseBaselinePath := filepath.Join(parseRunnerRootPath, "benchmarks", "quality_baseline.json")
	parseBaselineBytes, parseBaselineReadError := os.ReadFile(parseBaselinePath)
	if parseBaselineReadError != nil {
		return fmt.Errorf("failed to read quality baseline %q: %w", parseBaselinePath, parseBaselineReadError)
	}

	var parseBaselineSnapshot storeRunnerBenchmarkSnapshot
	if parseBaselineDecodeError := json.Unmarshal(parseBaselineBytes, &parseBaselineSnapshot); parseBaselineDecodeError != nil {
		return fmt.Errorf("failed to decode quality baseline %q: %w", parseBaselinePath, parseBaselineDecodeError)
	}

	parseBenchmarkOutput, buildRunnerBenchmarkOutputError := buildRunnerBenchmarkOutput(parseRunnerContext, parseRunnerRootPath)
	if buildRunnerBenchmarkOutputError != nil {
		return buildRunnerBenchmarkOutputError
	}

	storeCurrentBenchmarkRecords, parseRunnerBenchmarkRecordsError := parseRunnerBenchmarkRecords(parseBenchmarkOutput)
	if parseRunnerBenchmarkRecordsError != nil {
		return parseRunnerBenchmarkRecordsError
	}

	parseCurrentGoVersion, getRunnerGoVersionError := getRunnerGoVersion(parseRunnerContext, parseRunnerRootPath)
	if getRunnerGoVersionError != nil {
		return getRunnerGoVersionError
	}

	storeTrendBenchmarks := make(map[string]storeRunnerBenchmarkTrendRecord, len(parseBaselineSnapshot.StoreBenchmarks))
	storeBenchmarkNames := make([]string, 0, len(parseBaselineSnapshot.StoreBenchmarks))
	for parseBenchmarkName := range parseBaselineSnapshot.StoreBenchmarks {
		storeBenchmarkNames = append(storeBenchmarkNames, parseBenchmarkName)
	}
	sort.Strings(storeBenchmarkNames)

	for _, parseBenchmarkName := range storeBenchmarkNames {
		parseBaselineRecord := parseBaselineSnapshot.StoreBenchmarks[parseBenchmarkName]
		parseCurrentRecord, hasCurrentRecord := storeCurrentBenchmarkRecords[parseBenchmarkName]
		if !hasCurrentRecord {
			return fmt.Errorf("benchmark trend failed: missing current benchmark %q", parseBenchmarkName)
		}

		storeTrendBenchmarks[parseBenchmarkName] = storeRunnerBenchmarkTrendRecord{
			GetBenchmarkName:              parseBenchmarkName,
			GetNsPerOpDeltaPercent:        buildRunnerPercentDelta(parseBaselineRecord.GetNsPerOp, parseCurrentRecord.GetNsPerOp),
			GetBytesPerOpDeltaPercent:     buildRunnerPercentDelta(parseBaselineRecord.GetBytesPerOp, parseCurrentRecord.GetBytesPerOp),
			GetAllocsPerOpDeltaPercent:    buildRunnerPercentDelta(parseBaselineRecord.GetAllocsPerOp, parseCurrentRecord.GetAllocsPerOp),
			GetKilobytesPerOpDeltaPercent: buildRunnerPercentDelta(parseBaselineRecord.GetKilobytesPerOp, parseCurrentRecord.GetKilobytesPerOp),
		}
	}

	parseTrendSummary := storeRunnerBenchmarkTrendSummary{
		GetGeneratedAtUTC:         time.Now().UTC().Format(time.RFC3339),
		GetBaselineGeneratedAtUTC: parseBaselineSnapshot.GetGeneratedAtUTC,
		GetBaselineGoVersion:      parseBaselineSnapshot.GetGoVersion,
		GetCurrentGoVersion:       parseCurrentGoVersion,
		StoreBenchmarks:           storeTrendBenchmarks,
	}

	if storeRunnerTrendSummaryError := storeRunnerBenchmarkTrendReport(parseRunnerRootPath, parseTrendSummary); storeRunnerTrendSummaryError != nil {
		return storeRunnerTrendSummaryError
	}

	_, _ = fmt.Fprintf(os.Stdout, "benchmark trend summary captured against baseline: %s\n", parseBaselinePath)
	return nil
}

// buildRunnerPercentDelta reports percent delta from baseline to current. Positive values indicate growth.
func buildRunnerPercentDelta(parseBaselineValue float64, parseCurrentValue float64) float64 {
	if parseBaselineValue == 0 {
		if parseCurrentValue == 0 {
			return 0
		}
		return 100
	}
	return ((parseCurrentValue - parseBaselineValue) / parseBaselineValue) * 100
}

// buildRunnerCoveragePercent extracts the total statement coverage from coverage.txt.
func buildRunnerCoveragePercent(parseRunnerContext context.Context, parseRunnerRootPath string) (float64, error) {
	parseCoverageOutput, buildRunnerCoverageOutputError := buildRunnerProcessOutput(parseRunnerContext, parseRunnerRootPath, parseRunnerRootPath, nil, "go", "tool", "cover", "-func=coverage.txt")
	if buildRunnerCoverageOutputError != nil {
		return 0, buildRunnerCoverageOutputError
	}

	parseCoveragePattern := regexp.MustCompile(`total:\s+\(statements\)\s+([0-9.]+)%`)
	parseCoverageMatch := parseCoveragePattern.FindStringSubmatch(parseCoverageOutput)
	if len(parseCoverageMatch) != 2 {
		return 0, fmt.Errorf("failed to parse coverage total from output")
	}

	parseCoveragePercent, parseCoveragePercentError := strconv.ParseFloat(parseCoverageMatch[1], 64)
	if parseCoveragePercentError != nil {
		return 0, fmt.Errorf("failed to parse coverage value %q: %w", parseCoverageMatch[1], parseCoveragePercentError)
	}
	return parseCoveragePercent, nil
}

// buildRunnerBenchmarkOutput runs the benchmark subset used by quality gates.
func buildRunnerBenchmarkOutput(parseRunnerContext context.Context, parseRunnerRootPath string) (string, error) {
	parseBenchmarkPattern := "Benchmark(GRPC_(PayloadSize_1000Items|StreamLargeDataset_1000Items|BidirectionalStream_100Messages)|REST_(PayloadSize_1000Items|LargeDataset_1000Items|Bidirectional_100Messages))$"
	return buildRunnerProcessOutput(
		parseRunnerContext,
		parseRunnerRootPath,
		parseRunnerRootPath,
		nil,
		"go",
		"test",
		"./benchmarks",
		"-bench",
		parseBenchmarkPattern,
		"-run",
		"^$",
		"-benchmem",
		"-benchtime=300ms",
		"-count=1",
	)
}

// parseRunnerBenchmarkRecords parses benchmark output into normalized metric records.
func parseRunnerBenchmarkRecords(parseBenchmarkOutput string) (map[string]storeRunnerBenchmarkRecord, error) {
	storeBenchmarkRecords := map[string]storeRunnerBenchmarkRecord{}
	for _, parseLine := range strings.Split(parseBenchmarkOutput, "\n") {
		parseLine = strings.TrimSpace(parseLine)
		if !strings.HasPrefix(parseLine, "Benchmark") {
			continue
		}

		parseFields := strings.Fields(parseLine)
		if len(parseFields) < 8 {
			continue
		}

		parseBenchmarkName := parseRunnerBenchmarkName(parseFields[0])
		parseRecord := storeRunnerBenchmarkRecord{
			GetBenchmarkName: parseBenchmarkName,
		}

		for parseIndex := 2; parseIndex+1 < len(parseFields); parseIndex += 2 {
			parseMetricValue := parseFields[parseIndex]
			parseMetricUnit := parseFields[parseIndex+1]
			parseMetricFloat, parseMetricFloatError := strconv.ParseFloat(parseMetricValue, 64)
			if parseMetricFloatError != nil {
				continue
			}

			switch parseMetricUnit {
			case "ns/op":
				parseRecord.GetNsPerOp = parseMetricFloat
			case "B/op":
				parseRecord.GetBytesPerOp = parseMetricFloat
			case "allocs/op":
				parseRecord.GetAllocsPerOp = parseMetricFloat
			case "KB/op":
				parseRecord.GetKilobytesPerOp = parseMetricFloat
				parseRecord.HasKilobytesMetric = true
			}
		}

		storeBenchmarkRecords[parseBenchmarkName] = parseRecord
	}

	if len(storeBenchmarkRecords) == 0 {
		return nil, fmt.Errorf("no benchmark records were parsed")
	}
	return storeBenchmarkRecords, nil
}

// parseRunnerBenchmarkName strips the CPU suffix from benchmark names.
func parseRunnerBenchmarkName(parseRawBenchmarkName string) string {
	parseSuffixPattern := regexp.MustCompile(`-\d+$`)
	return parseSuffixPattern.ReplaceAllString(parseRawBenchmarkName, "")
}

// buildRunnerBenchmarkGateResult validates benchmark value-proposition invariants and returns summary metrics.
func buildRunnerBenchmarkGateResult(storeRunnerBenchmarkRecords map[string]storeRunnerBenchmarkRecord) (storeRunnerBenchmarkGateResult, error) {
	parseGRPCPayloadRecord, hasGRPCPayloadRecord := storeRunnerBenchmarkRecords["BenchmarkGRPC_PayloadSize_1000Items"]
	parseRESTPayloadRecord, hasRESTPayloadRecord := storeRunnerBenchmarkRecords["BenchmarkREST_PayloadSize_1000Items"]
	parseGRPCBidiRecord, hasGRPCBidiRecord := storeRunnerBenchmarkRecords["BenchmarkGRPC_BidirectionalStream_100Messages"]
	parseRESTBidiRecord, hasRESTBidiRecord := storeRunnerBenchmarkRecords["BenchmarkREST_Bidirectional_100Messages"]
	parseGRPCLargeRecord, hasGRPCLargeRecord := storeRunnerBenchmarkRecords["BenchmarkGRPC_StreamLargeDataset_1000Items"]
	parseRESTLargeRecord, hasRESTLargeRecord := storeRunnerBenchmarkRecords["BenchmarkREST_LargeDataset_1000Items"]

	if !hasGRPCPayloadRecord || !hasRESTPayloadRecord || !hasGRPCBidiRecord || !hasRESTBidiRecord || !hasGRPCLargeRecord || !hasRESTLargeRecord {
		return storeRunnerBenchmarkGateResult{}, fmt.Errorf("benchmark gate failed: missing required benchmark records")
	}
	if !parseGRPCPayloadRecord.HasKilobytesMetric || !parseRESTPayloadRecord.HasKilobytesMetric {
		return storeRunnerBenchmarkGateResult{}, fmt.Errorf("benchmark gate failed: missing KB/op payload metrics")
	}
	if parseRESTPayloadRecord.GetKilobytesPerOp <= 0 || parseRESTBidiRecord.GetBytesPerOp <= 0 || parseRESTBidiRecord.GetAllocsPerOp <= 0 || parseRESTLargeRecord.GetBytesPerOp <= 0 {
		return storeRunnerBenchmarkGateResult{}, fmt.Errorf("benchmark gate failed: invalid REST benchmark denominator")
	}

	parsePayloadSavings := 1.0 - (parseGRPCPayloadRecord.GetKilobytesPerOp / parseRESTPayloadRecord.GetKilobytesPerOp)
	parseBidiMemorySavings := 1.0 - (parseGRPCBidiRecord.GetBytesPerOp / parseRESTBidiRecord.GetBytesPerOp)
	parseBidiAllocsSavings := 1.0 - (parseGRPCBidiRecord.GetAllocsPerOp / parseRESTBidiRecord.GetAllocsPerOp)
	parseLargeDatasetMemorySavings := 1.0 - (parseGRPCLargeRecord.GetBytesPerOp / parseRESTLargeRecord.GetBytesPerOp)

	if parsePayloadSavings*100 < getRunnerBenchmarkPayloadSavingsThresholdPercent {
		return storeRunnerBenchmarkGateResult{}, fmt.Errorf("benchmark gate failed: payload savings %.2f%% < %.2f%%", parsePayloadSavings*100, getRunnerBenchmarkPayloadSavingsThresholdPercent)
	}
	if parseBidiMemorySavings*100 < getRunnerBenchmarkBidiMemorySavingsThresholdPercent {
		return storeRunnerBenchmarkGateResult{}, fmt.Errorf("benchmark gate failed: bidi memory savings %.2f%% < %.2f%%", parseBidiMemorySavings*100, getRunnerBenchmarkBidiMemorySavingsThresholdPercent)
	}
	if parseBidiAllocsSavings*100 < getRunnerBenchmarkBidiAllocsSavingsThresholdPercent {
		return storeRunnerBenchmarkGateResult{}, fmt.Errorf("benchmark gate failed: bidi alloc savings %.2f%% < %.2f%%", parseBidiAllocsSavings*100, getRunnerBenchmarkBidiAllocsSavingsThresholdPercent)
	}
	if parseLargeDatasetMemorySavings*100 < getRunnerBenchmarkLargeMemoryThresholdPercent {
		return storeRunnerBenchmarkGateResult{}, fmt.Errorf("benchmark gate failed: large dataset memory savings %.2f%% < %.2f%%", parseLargeDatasetMemorySavings*100, getRunnerBenchmarkLargeMemoryThresholdPercent)
	}

	parseGateResult := storeRunnerBenchmarkGateResult{
		GetPayloadSavingsPercent:     parsePayloadSavings * 100,
		GetBidiMemorySavingsPercent:  parseBidiMemorySavings * 100,
		GetBidiAllocsSavingsPercent:  parseBidiAllocsSavings * 100,
		GetLargeMemorySavingsPercent: parseLargeDatasetMemorySavings * 100,
	}

	_, _ = fmt.Fprintf(
		os.Stdout,
		"benchmark gates passed: payload %.2f%%, bidi memory %.2f%%, bidi allocs %.2f%%, large-dataset memory %.2f%%\n",
		parseGateResult.GetPayloadSavingsPercent,
		parseGateResult.GetBidiMemorySavingsPercent,
		parseGateResult.GetBidiAllocsSavingsPercent,
		parseGateResult.GetLargeMemorySavingsPercent,
	)
	return parseGateResult, nil
}

// storeRunnerQualitySummary stores the latest quality gate summary for CI artifact upload.
func storeRunnerQualitySummary(parseRunnerRootPath string, parseCoveragePercent float64, parseBenchmarkGateResult storeRunnerBenchmarkGateResult) error {
	parseSummary := storeRunnerQualitySummaryPayload{
		GetGeneratedAtUTC:      time.Now().UTC().Format(time.RFC3339),
		GetCoveragePercent:     parseCoveragePercent,
		GetCoverageThreshold:   90.0,
		GetBenchmarkGateResult: parseBenchmarkGateResult,
	}
	parseSummaryBytes, parseSummaryMarshalError := json.MarshalIndent(parseSummary, "", "  ")
	if parseSummaryMarshalError != nil {
		return fmt.Errorf("failed to marshal quality summary: %w", parseSummaryMarshalError)
	}

	parseSummaryDirPath := filepath.Join(parseRunnerRootPath, "bin", "quality")
	if storeRunnerSummaryDirectoryError := os.MkdirAll(parseSummaryDirPath, 0o755); storeRunnerSummaryDirectoryError != nil {
		return fmt.Errorf("failed to create quality summary directory %q: %w", parseSummaryDirPath, storeRunnerSummaryDirectoryError)
	}

	parseSummaryPath := filepath.Join(parseSummaryDirPath, "summary.json")
	if storeRunnerSummaryError := os.WriteFile(parseSummaryPath, parseSummaryBytes, 0o644); storeRunnerSummaryError != nil {
		return fmt.Errorf("failed to write quality summary %q: %w", parseSummaryPath, storeRunnerSummaryError)
	}
	_, _ = fmt.Fprintf(os.Stdout, "stored quality summary: %s\n", parseSummaryPath)
	return nil
}

// storeRunnerBenchmarkTrendSummary stores benchmark trend comparison output for CI artifact upload.
func storeRunnerBenchmarkTrendReport(parseRunnerRootPath string, parseTrendSummary storeRunnerBenchmarkTrendSummary) error {
	parseTrendBytes, parseTrendMarshalError := json.MarshalIndent(parseTrendSummary, "", "  ")
	if parseTrendMarshalError != nil {
		return fmt.Errorf("failed to marshal benchmark trend summary: %w", parseTrendMarshalError)
	}

	parseSummaryDirPath := filepath.Join(parseRunnerRootPath, "bin", "quality")
	if storeRunnerSummaryDirectoryError := os.MkdirAll(parseSummaryDirPath, 0o755); storeRunnerSummaryDirectoryError != nil {
		return fmt.Errorf("failed to create trend summary directory %q: %w", parseSummaryDirPath, storeRunnerSummaryDirectoryError)
	}

	parseTrendPath := filepath.Join(parseSummaryDirPath, "trend.json")
	if storeRunnerTrendError := os.WriteFile(parseTrendPath, parseTrendBytes, 0o644); storeRunnerTrendError != nil {
		return fmt.Errorf("failed to write trend summary %q: %w", parseTrendPath, storeRunnerTrendError)
	}
	_, _ = fmt.Fprintf(os.Stdout, "stored benchmark trend summary: %s\n", parseTrendPath)
	return nil
}

// getRunnerGoVersion gets the installed Go version string.
func getRunnerGoVersion(parseRunnerContext context.Context, parseRunnerRootPath string) (string, error) {
	parseGoVersionOutput, buildRunnerGoVersionOutputError := buildRunnerProcessOutput(parseRunnerContext, parseRunnerRootPath, parseRunnerRootPath, nil, "go", "version")
	if buildRunnerGoVersionOutputError != nil {
		return "", buildRunnerGoVersionOutputError
	}
	return strings.TrimSpace(parseGoVersionOutput), nil
}

// isRunnerRaceToolchainIssue reports whether race-mode failures come from missing CGO/compiler support.
func isRunnerRaceToolchainIssue(parseOutput string) bool {
	parseOutputLower := strings.ToLower(parseOutput)
	return strings.Contains(parseOutputLower, "requires cgo") ||
		strings.Contains(parseOutputLower, "c compiler") ||
		strings.Contains(parseOutputLower, "gcc") ||
		strings.Contains(parseOutputLower, "clang")
}

// buildRunnerProcessOutput runs a process and returns combined stdout and stderr.
func buildRunnerProcessOutput(parseRunnerContext context.Context, parseRunnerRootPath string, parseRunnerWorkPath string, storeRunnerEnv map[string]string, parseRunnerBinaryName string, parseRunnerArgs ...string) (string, error) {
	parseRunnerCommand := exec.CommandContext(parseRunnerContext, parseRunnerBinaryName, parseRunnerArgs...)
	parseRunnerCommand.Dir = parseRunnerWorkPath
	parseRunnerCommand.Env = buildRunnerEnv(parseRunnerRootPath, storeRunnerEnv)

	var buildRunnerOutputBuffer bytes.Buffer
	parseRunnerCommand.Stdout = &buildRunnerOutputBuffer
	parseRunnerCommand.Stderr = &buildRunnerOutputBuffer

	if buildRunnerRunError := parseRunnerCommand.Run(); buildRunnerRunError != nil {
		return buildRunnerOutputBuffer.String(), fmt.Errorf("failed to execute %s %v: %w", parseRunnerBinaryName, parseRunnerArgs, buildRunnerRunError)
	}
	return buildRunnerOutputBuffer.String(), nil
}
