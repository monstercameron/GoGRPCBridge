package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type storeAPIGuardBaseline struct {
	GetGeneratedAtUTC string              `json:"generated_at_utc"`
	StorePackages     map[string][]string `json:"packages"`
}

// main executes the API compatibility guard.
func main() {
	if parseErr := handleAPIGuardMain(os.Args[1:]); parseErr != nil {
		_, _ = fmt.Fprintf(os.Stderr, "api guard error: %v\n", parseErr)
		os.Exit(1)
	}
}

// handleAPIGuardMain resolves the command and runs the API compatibility guard.
func handleAPIGuardMain(parseArguments []string) error {
	parseCommand := "check"
	if len(parseArguments) > 0 {
		parseCommand = strings.TrimSpace(parseArguments[0])
	}

	parseRepositoryRootPath, parseErr := getAPIGuardRepositoryRootPath()
	if parseErr != nil {
		return parseErr
	}

	parsePackageSymbols, parseErr := buildAPIGuardPackageSymbolMap(parseRepositoryRootPath, []string{
		"pkg/grpctunnel",
		"pkg/bridge",
	})
	if parseErr != nil {
		return parseErr
	}

	parseBaselinePath := filepath.Join(parseRepositoryRootPath, "tools", "api_compat_guard", "api_compatibility_baseline.json")
	switch parseCommand {
	case "check":
		if parseErr = handleAPIGuardDocumentationCheck(parseRepositoryRootPath); parseErr != nil {
			return parseErr
		}
		return handleAPIGuardBaselineCheck(parseBaselinePath, parsePackageSymbols)
	case "update":
		return storeAPIGuardBaselineFile(parseBaselinePath, parsePackageSymbols)
	default:
		return fmt.Errorf("unknown command %q; use check or update", parseCommand)
	}
}

// getAPIGuardRepositoryRootPath resolves the repository root from this source file location.
func getAPIGuardRepositoryRootPath() (string, error) {
	_, parseCurrentFilePath, _, hasCaller := runtime.Caller(0)
	if !hasCaller {
		return "", errors.New("failed to resolve api_compat_guard source location")
	}
	parseRepositoryRootPath := filepath.Clean(filepath.Join(filepath.Dir(parseCurrentFilePath), "..", ".."))
	if _, parseErr := os.Stat(filepath.Join(parseRepositoryRootPath, "go.mod")); parseErr != nil {
		return "", fmt.Errorf("failed to validate repository root %q: %w", parseRepositoryRootPath, parseErr)
	}
	return parseRepositoryRootPath, nil
}

// buildAPIGuardPackageSymbolMap parses package directories and returns exported API symbol snapshots.
func buildAPIGuardPackageSymbolMap(parseRepositoryRootPath string, parsePackagePaths []string) (map[string][]string, error) {
	parsePackages := map[string][]string{}
	for _, parsePackagePath := range parsePackagePaths {
		parsePackageSymbols, parseErr := buildAPIGuardPackageSymbols(filepath.Join(parseRepositoryRootPath, parsePackagePath))
		if parseErr != nil {
			return nil, parseErr
		}
		parsePackages[parsePackagePath] = parsePackageSymbols
	}
	return parsePackages, nil
}

// buildAPIGuardPackageSymbols parses one package directory and returns sorted unique exported symbol names.
func buildAPIGuardPackageSymbols(parsePackageDirectoryPath string) ([]string, error) {
	parseFileSet := token.NewFileSet()
	parsePackages, parseErr := parser.ParseDir(parseFileSet, parsePackageDirectoryPath, func(parseFileInfo os.FileInfo) bool {
		parseFileName := strings.TrimSpace(parseFileInfo.Name())
		if strings.HasSuffix(parseFileName, "_test.go") {
			return false
		}
		return strings.HasSuffix(parseFileName, ".go")
	}, 0)
	if parseErr != nil {
		return nil, fmt.Errorf("failed to parse package directory %q: %w", parsePackageDirectoryPath, parseErr)
	}

	parseSymbolSet := map[string]struct{}{}
	for _, parsePackage := range parsePackages {
		for _, parseFile := range parsePackage.Files {
			for _, parseDeclaration := range parseFile.Decls {
				storeAPIGuardDeclarationSymbols(parseSymbolSet, parseDeclaration)
			}
		}
	}

	parseSymbols := make([]string, 0, len(parseSymbolSet))
	for parseSymbol := range parseSymbolSet {
		parseSymbols = append(parseSymbols, parseSymbol)
	}
	sort.Strings(parseSymbols)
	return parseSymbols, nil
}

// storeAPIGuardDeclarationSymbols records exported symbol names for one declaration.
func storeAPIGuardDeclarationSymbols(parseSymbolSet map[string]struct{}, parseDeclaration ast.Decl) {
	switch parseTypedDeclaration := parseDeclaration.(type) {
	case *ast.GenDecl:
		storeAPIGuardGenDeclarationSymbols(parseSymbolSet, parseTypedDeclaration)
	case *ast.FuncDecl:
		storeAPIGuardFuncDeclarationSymbol(parseSymbolSet, parseTypedDeclaration)
	}
}

// storeAPIGuardGenDeclarationSymbols records exported symbol names for const/var/type declarations.
func storeAPIGuardGenDeclarationSymbols(parseSymbolSet map[string]struct{}, parseDeclaration *ast.GenDecl) {
	if parseSymbolSet == nil || parseDeclaration == nil {
		return
	}

	switch parseDeclaration.Tok {
	case token.TYPE:
		for _, parseSpec := range parseDeclaration.Specs {
			parseTypeSpec, isTypeSpec := parseSpec.(*ast.TypeSpec)
			if !isTypeSpec || !parseTypeSpec.Name.IsExported() {
				continue
			}
			parseSymbolSet["type "+parseTypeSpec.Name.Name] = struct{}{}
		}
	case token.CONST:
		for _, parseSpec := range parseDeclaration.Specs {
			parseValueSpec, isValueSpec := parseSpec.(*ast.ValueSpec)
			if !isValueSpec {
				continue
			}
			for _, parseName := range parseValueSpec.Names {
				if parseName.IsExported() {
					parseSymbolSet["const "+parseName.Name] = struct{}{}
				}
			}
		}
	case token.VAR:
		for _, parseSpec := range parseDeclaration.Specs {
			parseValueSpec, isValueSpec := parseSpec.(*ast.ValueSpec)
			if !isValueSpec {
				continue
			}
			for _, parseName := range parseValueSpec.Names {
				if parseName.IsExported() {
					parseSymbolSet["var "+parseName.Name] = struct{}{}
				}
			}
		}
	}
}

// storeAPIGuardFuncDeclarationSymbol records exported function and method symbols.
func storeAPIGuardFuncDeclarationSymbol(parseSymbolSet map[string]struct{}, parseDeclaration *ast.FuncDecl) {
	if parseSymbolSet == nil || parseDeclaration == nil || parseDeclaration.Name == nil || !parseDeclaration.Name.IsExported() {
		return
	}

	if parseDeclaration.Recv == nil || len(parseDeclaration.Recv.List) == 0 {
		parseSymbolSet["func "+parseDeclaration.Name.Name] = struct{}{}
		return
	}

	parseReceiverType := getAPIGuardReceiverTypeName(parseDeclaration.Recv.List[0].Type)
	parseSymbolSet["method ("+parseReceiverType+") "+parseDeclaration.Name.Name] = struct{}{}
}

// getAPIGuardReceiverTypeName resolves a stable method receiver name for symbol snapshots.
func getAPIGuardReceiverTypeName(parseTypeExpression ast.Expr) string {
	switch parseTypedExpression := parseTypeExpression.(type) {
	case *ast.StarExpr:
		return "*" + getAPIGuardReceiverTypeName(parseTypedExpression.X)
	case *ast.Ident:
		return parseTypedExpression.Name
	case *ast.IndexExpr:
		return getAPIGuardReceiverTypeName(parseTypedExpression.X)
	case *ast.IndexListExpr:
		return getAPIGuardReceiverTypeName(parseTypedExpression.X)
	case *ast.SelectorExpr:
		return parseTypedExpression.Sel.Name
	default:
		return "unknown"
	}
}

// handleAPIGuardDocumentationCheck validates migration and compatibility documentation sections required by policy.
func handleAPIGuardDocumentationCheck(parseRepositoryRootPath string) error {
	parseChecks := []struct {
		parsePaths    []string
		parsePatterns []string
	}{
		{
			parsePaths: []string{
				"docs/core/API_COMPATIBILITY.md",
				"API_COMPATIBILITY.md",
			},
			parsePatterns: []string{
				"## Deprecation Lifecycle",
				"## CI and Release Enforcement",
			},
		},
		{
			parsePaths: []string{
				"docs/core/MIGRATION.md",
				"MIGRATION.md",
			},
			parsePatterns: []string{
				"## Old to New Mapping",
			},
		},
	}

	for _, parseCheck := range parseChecks {
		parseDocumentationPath, parseErr := getAPIGuardDocumentationPath(parseRepositoryRootPath, parseCheck.parsePaths)
		if parseErr != nil {
			return parseErr
		}

		parseContents, parseErr := os.ReadFile(parseDocumentationPath)
		if parseErr != nil {
			return fmt.Errorf("failed to read documentation file %q: %w", parseDocumentationPath, parseErr)
		}
		parseText := string(parseContents)
		for _, parsePattern := range parseCheck.parsePatterns {
			if !strings.Contains(parseText, parsePattern) {
				return fmt.Errorf("documentation guard failed: file %q is missing section %q", parseDocumentationPath, parsePattern)
			}
		}
	}
	return nil
}

// getAPIGuardDocumentationPath resolves the first existing documentation path from ordered candidates.
func getAPIGuardDocumentationPath(parseRepositoryRootPath string, parseRelativePaths []string) (string, error) {
	storeCheckedPaths := make([]string, 0, len(parseRelativePaths))
	for _, parseRelativePath := range parseRelativePaths {
		parseDocumentationPath := filepath.Join(parseRepositoryRootPath, parseRelativePath)
		storeCheckedPaths = append(storeCheckedPaths, parseDocumentationPath)
		if _, parseErr := os.Stat(parseDocumentationPath); parseErr == nil {
			return parseDocumentationPath, nil
		}
	}
	return "", fmt.Errorf("failed to locate documentation file; checked: %s", strings.Join(storeCheckedPaths, ", "))
}

// handleAPIGuardBaselineCheck compares current package symbols against the stored compatibility baseline.
func handleAPIGuardBaselineCheck(parseBaselinePath string, parseCurrentSymbols map[string][]string) error {
	parseBaselineBytes, parseErr := os.ReadFile(parseBaselinePath)
	if parseErr != nil {
		return fmt.Errorf("failed to read API baseline %q: %w", parseBaselinePath, parseErr)
	}

	parseBaseline := storeAPIGuardBaseline{}
	if parseErr = json.Unmarshal(parseBaselineBytes, &parseBaseline); parseErr != nil {
		return fmt.Errorf("failed to parse API baseline %q: %w", parseBaselinePath, parseErr)
	}

	var storeMissingSymbols []string
	for parsePackagePath, parseBaselineSymbols := range parseBaseline.StorePackages {
		parseCurrentPackageSymbols := parseCurrentSymbols[parsePackagePath]
		parseCurrentSet := map[string]struct{}{}
		for _, parseSymbol := range parseCurrentPackageSymbols {
			parseCurrentSet[parseSymbol] = struct{}{}
		}

		for _, parseSymbol := range parseBaselineSymbols {
			if _, hasSymbol := parseCurrentSet[parseSymbol]; !hasSymbol {
				storeMissingSymbols = append(storeMissingSymbols, parsePackagePath+"::"+parseSymbol)
			}
		}
	}

	if len(storeMissingSymbols) > 0 {
		sort.Strings(storeMissingSymbols)
		return fmt.Errorf(
			"API compatibility guard failed; missing exported symbols:\n- %s\nIf this change is intentional, update MIGRATION.md and regenerate baseline with: go run ./tools/api_compat_guard update",
			strings.Join(storeMissingSymbols, "\n- "),
		)
	}

	_, _ = fmt.Fprintf(os.Stdout, "API compatibility guard passed for %d package baselines.\n", len(parseBaseline.StorePackages))
	return nil
}

// storeAPIGuardBaselineFile writes the current API symbol baseline to disk.
func storeAPIGuardBaselineFile(parseBaselinePath string, parseCurrentSymbols map[string][]string) error {
	parseBaseline := storeAPIGuardBaseline{
		GetGeneratedAtUTC: time.Now().UTC().Format(time.RFC3339),
		StorePackages:     parseCurrentSymbols,
	}

	parseBaselineBytes, parseErr := json.MarshalIndent(parseBaseline, "", "  ")
	if parseErr != nil {
		return fmt.Errorf("failed to marshal API baseline: %w", parseErr)
	}
	parseBaselineBytes = append(parseBaselineBytes, '\n')

	if parseErr = os.WriteFile(parseBaselinePath, parseBaselineBytes, 0o644); parseErr != nil {
		return fmt.Errorf("failed to write API baseline %q: %w", parseBaselinePath, parseErr)
	}

	_, _ = fmt.Fprintf(os.Stdout, "stored API compatibility baseline: %s\n", parseBaselinePath)
	return nil
}
