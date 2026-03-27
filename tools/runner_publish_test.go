package main

import "testing"

func TestParseRunnerGoModModulePath(parseT *testing.T) {
	parseModulePath, parseErr := parseRunnerGoModModulePath("module github.com/monstercameron/GoGRPCBridge\n\ngo 1.25.0\n")
	if parseErr != nil {
		parseT.Fatalf("parseRunnerGoModModulePath() error = %v, want nil", parseErr)
	}
	if parseModulePath != "github.com/monstercameron/GoGRPCBridge" {
		parseT.Fatalf("parseRunnerGoModModulePath() = %q, want %q", parseModulePath, "github.com/monstercameron/GoGRPCBridge")
	}
}

func TestParseRunnerGoModModulePath_MissingModuleLine(parseT *testing.T) {
	_, parseErr := parseRunnerGoModModulePath("go 1.25.0\n")
	if parseErr == nil {
		parseT.Fatal("parseRunnerGoModModulePath() expected error, got nil")
	}
}

func TestNormalizeRunnerRepositoryURL(parseT *testing.T) {
	parseTests := []struct {
		parseName     string
		parseInput    string
		parseExpected string
	}{
		{
			parseName:     "https with git suffix",
			parseInput:    "https://github.com/monstercameron/GoGRPCBridge.git",
			parseExpected: "https://github.com/monstercameron/GoGRPCBridge",
		},
		{
			parseName:     "ssh url",
			parseInput:    "git@github.com:monstercameron/GoGRPCBridge.git",
			parseExpected: "https://github.com/monstercameron/GoGRPCBridge",
		},
		{
			parseName:     "already normalized",
			parseInput:    "https://github.com/monstercameron/GoGRPCBridge",
			parseExpected: "https://github.com/monstercameron/GoGRPCBridge",
		},
		{
			parseName:     "canonical repository",
			parseInput:    "https://github.com/monstercameron/GoGRPCBridge.git",
			parseExpected: "https://github.com/monstercameron/GoGRPCBridge",
		},
	}

	for _, parseTestCase := range parseTests {
		parseT.Run(parseTestCase.parseName, func(parseT2 *testing.T) {
			parseNormalizedURL := normalizeRunnerRepositoryURL(parseTestCase.parseInput)
			if parseNormalizedURL != parseTestCase.parseExpected {
				parseT2.Fatalf("normalizeRunnerRepositoryURL() = %q, want %q", parseNormalizedURL, parseTestCase.parseExpected)
			}
		})
	}
}

func TestBuildRunnerCanonicalEnv_Default(parseT *testing.T) {
	parseT.Setenv(parseRunnerCanonicalGoProxyEnvKey, "")
	parseEnv := buildRunnerCanonicalEnv()
	if parseEnv != nil {
		parseT.Fatalf("buildRunnerCanonicalEnv() = %v, want nil", parseEnv)
	}
}

func TestBuildRunnerCanonicalEnv_WithGoProxy(parseT *testing.T) {
	parseT.Setenv(parseRunnerCanonicalGoProxyEnvKey, "direct")
	parseEnv := buildRunnerCanonicalEnv()
	if parseEnv == nil {
		parseT.Fatal("buildRunnerCanonicalEnv() = nil, want map")
	}
	if parseEnv["GOPROXY"] != "direct" {
		parseT.Fatalf("buildRunnerCanonicalEnv()[GOPROXY] = %q, want %q", parseEnv["GOPROXY"], "direct")
	}
}

func TestBuildRunnerCanonicalRepositoryURLs(parseT *testing.T) {
	storeRunnerURLs := buildRunnerCanonicalRepositoryURLs()
	if len(storeRunnerURLs) != 1 {
		parseT.Fatalf("buildRunnerCanonicalRepositoryURLs() len = %d, want %d", len(storeRunnerURLs), 1)
	}
	if storeRunnerURLs[0] != "https://github.com/monstercameron/GoGRPCBridge" {
		parseT.Fatalf("buildRunnerCanonicalRepositoryURLs()[0] = %q, want %q", storeRunnerURLs[0], "https://github.com/monstercameron/GoGRPCBridge")
	}
}

func TestHasRunnerCanonicalRepositoryURL(parseT *testing.T) {
	storeRunnerURLs := buildRunnerCanonicalRepositoryURLs()
	if !hasRunnerCanonicalRepositoryURL("git@github.com:monstercameron/GoGRPCBridge.git", storeRunnerURLs) {
		parseT.Fatal("hasRunnerCanonicalRepositoryURL() = false, want true for canonical URL")
	}
	if hasRunnerCanonicalRepositoryURL("https://github.com/monstercameron/grpc-tunnel.git", storeRunnerURLs) {
		parseT.Fatal("hasRunnerCanonicalRepositoryURL() = true, want false for non-canonical URL")
	}
	if hasRunnerCanonicalRepositoryURL("https://github.com/monstercameron/not-this-one", storeRunnerURLs) {
		parseT.Fatal("hasRunnerCanonicalRepositoryURL() = true, want false for unknown URL")
	}
}

func TestHasRunnerSkipCanonicalOriginCheck(parseT *testing.T) {
	parseT.Setenv(parseRunnerCanonicalSkipOriginEnvKey, "")
	if hasRunnerSkipCanonicalOriginCheck() {
		parseT.Fatal("hasRunnerSkipCanonicalOriginCheck() = true, want false")
	}

	parseT.Setenv(parseRunnerCanonicalSkipOriginEnvKey, "1")
	if !hasRunnerSkipCanonicalOriginCheck() {
		parseT.Fatal("hasRunnerSkipCanonicalOriginCheck() = false, want true")
	}

	parseT.Setenv(parseRunnerCanonicalSkipOriginEnvKey, "true")
	if !hasRunnerSkipCanonicalOriginCheck() {
		parseT.Fatal("hasRunnerSkipCanonicalOriginCheck() = false, want true")
	}
}

func TestBuildRunnerCanonicalWASMEnv(parseT *testing.T) {
	storeRunnerEnv := buildRunnerCanonicalWASMEnv(map[string]string{
		"GOPROXY": "direct",
	})
	if storeRunnerEnv["GOOS"] != "js" {
		parseT.Fatalf("buildRunnerCanonicalWASMEnv()[GOOS] = %q, want %q", storeRunnerEnv["GOOS"], "js")
	}
	if storeRunnerEnv["GOARCH"] != "wasm" {
		parseT.Fatalf("buildRunnerCanonicalWASMEnv()[GOARCH] = %q, want %q", storeRunnerEnv["GOARCH"], "wasm")
	}
	if storeRunnerEnv["GOPROXY"] != "direct" {
		parseT.Fatalf("buildRunnerCanonicalWASMEnv()[GOPROXY] = %q, want %q", storeRunnerEnv["GOPROXY"], "direct")
	}
}
