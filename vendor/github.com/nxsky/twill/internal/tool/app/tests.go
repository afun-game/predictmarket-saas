// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package app

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

const testsSchemaVersion = "twill.app.tests.v1"

// Tests describes the static test files discovered for a Twill application.
type Tests struct {
	SchemaVersion string           `json:"schema_version"`
	Packages      []TestPackage    `json:"packages"`
	Components    []ComponentTests `json:"components"`
	Strategies    []TestStrategy   `json:"strategies"`
	Coverage      CoverageSummary  `json:"coverage"`
	Limitations   []string         `json:"limitations"`
}

// TestPackage describes package-local and external Go test files.
type TestPackage struct {
	Path               string   `json:"path"`
	Dir                string   `json:"dir"`
	TestFiles          []string `json:"test_files"`
	ExternalTestFiles  []string `json:"external_test_files"`
	TestFunctions      []string `json:"test_functions"`
	FuzzFunctions      []string `json:"fuzz_functions"`
	BenchmarkFunctions []string `json:"benchmark_functions"`
}

// ComponentTests describes package-level test hints for one Twill component.
type ComponentTests struct {
	Component          string   `json:"component"`
	Package            string   `json:"package"`
	Status             string   `json:"status"`
	TestFiles          []string `json:"test_files"`
	ExternalTestFiles  []string `json:"external_test_files"`
	TestFunctions      []string `json:"test_functions"`
	FuzzFunctions      []string `json:"fuzz_functions"`
	BenchmarkFunctions []string `json:"benchmark_functions"`
}

// TestStrategy describes a safe static testing strategy signal.
type TestStrategy struct {
	Name           string   `json:"name"`
	Status         string   `json:"status"`
	Evidence       []string `json:"evidence"`
	VerifyCommands []string `json:"verify_commands"`
	Notes          []string `json:"notes"`
}

// CoverageSummary describes existing Go coverage profile evidence without running tests.
type CoverageSummary struct {
	Status            string   `json:"status"`
	Files             []string `json:"files"`
	Mode              string   `json:"mode,omitempty"`
	Packages          []string `json:"packages"`
	Statements        int      `json:"statements"`
	CoveredStatements int      `json:"covered_statements"`
	Percent           float64  `json:"percent"`
	Limitations       []string `json:"limitations"`
}

// InspectTests inspects packages and returns their static test file inventory.
func InspectTests(ctx context.Context, opts GraphOptions) (*Tests, error) {
	graph, err := InspectGraph(ctx, opts)
	if err != nil {
		return nil, err
	}
	dir := packageLoadDir(opts)
	rootDir, err := inspectionRootDir(opts)
	if err != nil {
		return nil, err
	}
	patterns := opts.Patterns
	if len(patterns) == 0 {
		patterns = []string{"."}
	}

	cfg := &packages.Config{
		Context: ctx,
		Dir:     dir,
		Mode:    packages.NeedName | packages.NeedFiles,
		Tests:   true,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}

	index := map[string]*TestPackage{}
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			return nil, packageErrors(pkg)
		}
		inspectTestPackage(rootDir, index, pkg)
	}

	tests := &Tests{
		SchemaVersion: testsSchemaVersion,
		Packages:      []TestPackage{},
		Components:    []ComponentTests{},
		Strategies:    []TestStrategy{},
		Coverage:      inspectCoverageSummary(rootDir),
		Limitations: []string{
			"Component test status is a package-level heuristic based on Twill component package names.",
			"Function lists are parsed from *_test.go declarations; runtime test results are not computed.",
			"Coverage summaries are parsed only from existing local Go coverage profile files; Twill does not run tests or write coverage files during inspection.",
			"Package tests may cover shared helpers or multiple components and are not proof of behavior-level coverage.",
			"Testing strategy summaries are static hints based on test declarations and filenames; they do not prove randomized coverage quality.",
		},
	}
	for _, pkg := range index {
		pkg.TestFiles = uniqueSortedStrings(pkg.TestFiles)
		pkg.ExternalTestFiles = uniqueSortedStrings(pkg.ExternalTestFiles)
		pkg.TestFunctions = uniqueSortedStrings(pkg.TestFunctions)
		pkg.FuzzFunctions = uniqueSortedStrings(pkg.FuzzFunctions)
		pkg.BenchmarkFunctions = uniqueSortedStrings(pkg.BenchmarkFunctions)
		tests.Packages = append(tests.Packages, *pkg)
	}
	sort.Slice(tests.Packages, func(i, j int) bool {
		return tests.Packages[i].Path < tests.Packages[j].Path
	})
	tests.Components = componentTestHints(graph, index)
	tests.Strategies = testStrategies(tests.Packages, patterns)
	return tests, nil
}

func inspectTestPackage(rootDir string, index map[string]*TestPackage, pkg *packages.Package) {
	path := testPackagePath(pkg)
	if path == "" {
		return
	}

	testFiles := []string{}
	testFunctions := []string{}
	fuzzFunctions := []string{}
	benchmarkFunctions := []string{}
	for _, filename := range pkg.GoFiles {
		if strings.HasSuffix(filename, "_test.go") {
			testFiles = append(testFiles, cleanPath(rootDir, filename))
			functions := testFunctionsInFile(filename)
			testFunctions = append(testFunctions, functions.Tests...)
			fuzzFunctions = append(fuzzFunctions, functions.Fuzzes...)
			benchmarkFunctions = append(benchmarkFunctions, functions.Benchmarks...)
		}
	}
	if len(testFiles) == 0 {
		return
	}

	entry, ok := index[path]
	if !ok {
		entry = &TestPackage{
			Path:               path,
			Dir:                cleanPath(rootDir, pkg.Dir),
			TestFiles:          []string{},
			ExternalTestFiles:  []string{},
			TestFunctions:      []string{},
			FuzzFunctions:      []string{},
			BenchmarkFunctions: []string{},
		}
		index[path] = entry
	}
	entry.TestFunctions = append(entry.TestFunctions, testFunctions...)
	entry.FuzzFunctions = append(entry.FuzzFunctions, fuzzFunctions...)
	entry.BenchmarkFunctions = append(entry.BenchmarkFunctions, benchmarkFunctions...)
	if strings.HasSuffix(pkg.Name, "_test") {
		entry.ExternalTestFiles = append(entry.ExternalTestFiles, testFiles...)
		return
	}
	entry.TestFiles = append(entry.TestFiles, testFiles...)
}

func testPackagePath(pkg *packages.Package) string {
	if pkg.ForTest != "" {
		return pkg.ForTest
	}
	if strings.HasSuffix(pkg.PkgPath, ".test") {
		return ""
	}
	return strings.TrimSuffix(pkg.PkgPath, "_test")
}

type parsedTestFunctions struct {
	Tests      []string
	Fuzzes     []string
	Benchmarks []string
}

func testFunctionsInFile(filename string) parsedTestFunctions {
	data, err := os.ReadFile(filename)
	if err != nil {
		return parsedTestFunctions{
			Tests:      []string{},
			Fuzzes:     []string{},
			Benchmarks: []string{},
		}
	}
	file, err := parser.ParseFile(token.NewFileSet(), filename, data, 0)
	if err != nil {
		return parsedTestFunctions{
			Tests:      []string{},
			Fuzzes:     []string{},
			Benchmarks: []string{},
		}
	}
	functions := parsedTestFunctions{
		Tests:      []string{},
		Fuzzes:     []string{},
		Benchmarks: []string{},
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		switch {
		case strings.HasPrefix(fn.Name.Name, "Test"):
			functions.Tests = append(functions.Tests, fn.Name.Name)
		case strings.HasPrefix(fn.Name.Name, "Fuzz"):
			functions.Fuzzes = append(functions.Fuzzes, fn.Name.Name)
		case strings.HasPrefix(fn.Name.Name, "Benchmark"):
			functions.Benchmarks = append(functions.Benchmarks, fn.Name.Name)
		}
	}
	functions.Tests = uniqueSortedStrings(functions.Tests)
	functions.Fuzzes = uniqueSortedStrings(functions.Fuzzes)
	functions.Benchmarks = uniqueSortedStrings(functions.Benchmarks)
	return functions
}

func componentTestHints(graph *Graph, packages map[string]*TestPackage) []ComponentTests {
	hints := make([]ComponentTests, 0, len(graph.Components))
	for _, component := range graph.Components {
		pkg := packages[component.Package]
		hint := ComponentTests{
			Component:          component.Name,
			Package:            component.Package,
			Status:             "no_package_tests",
			TestFiles:          []string{},
			ExternalTestFiles:  []string{},
			TestFunctions:      []string{},
			FuzzFunctions:      []string{},
			BenchmarkFunctions: []string{},
		}
		if pkg != nil {
			hint.Status = "package_tests_present"
			hint.TestFiles = append([]string{}, pkg.TestFiles...)
			hint.ExternalTestFiles = append([]string{}, pkg.ExternalTestFiles...)
			hint.TestFunctions = append([]string{}, pkg.TestFunctions...)
			hint.FuzzFunctions = append([]string{}, pkg.FuzzFunctions...)
			hint.BenchmarkFunctions = append([]string{}, pkg.BenchmarkFunctions...)
		}
		hints = append(hints, hint)
	}
	sort.Slice(hints, func(i, j int) bool {
		return hints[i].Component < hints[j].Component
	})
	return hints
}

func testStrategies(packages []TestPackage, patterns []string) []TestStrategy {
	if len(patterns) == 0 {
		patterns = []string{"."}
	}

	unitEvidence := []string{}
	fuzzEvidence := []string{}
	benchmarkEvidence := []string{}
	simulationEvidence := []string{}
	chaosEvidence := []string{}
	for _, pkg := range packages {
		for _, name := range pkg.TestFunctions {
			unitEvidence = append(unitEvidence, pkg.Path+"."+name)
		}
		for _, name := range pkg.FuzzFunctions {
			fuzzEvidence = append(fuzzEvidence, pkg.Path+"."+name)
		}
		for _, name := range pkg.BenchmarkFunctions {
			benchmarkEvidence = append(benchmarkEvidence, pkg.Path+"."+name)
		}
		for _, name := range pkg.TestFunctions {
			lower := strings.ToLower(name)
			if matchesSimulationTestSignal(lower) {
				simulationEvidence = append(simulationEvidence, pkg.Path+"."+name)
			}
			if matchesChaosTestSignal(lower) {
				chaosEvidence = append(chaosEvidence, pkg.Path+"."+name)
			}
		}
		for _, file := range append(append([]string{}, pkg.TestFiles...), pkg.ExternalTestFiles...) {
			lower := strings.ToLower(file)
			if matchesSimulationTestSignal(lower) {
				simulationEvidence = append(simulationEvidence, pkg.Path+":"+file)
			}
			if matchesChaosTestSignal(lower) {
				chaosEvidence = append(chaosEvidence, pkg.Path+":"+file)
			}
		}
	}

	return []TestStrategy{
		testStrategy("unit", unitEvidence, []string{goTestVerifyCommand(patterns)}, []string{
			"Detected from Test* functions in Go test files.",
		}),
		testStrategy("fuzz_property", fuzzEvidence, []string{goTestFuzzVerifyCommand(patterns)}, []string{
			"Detected from Fuzz* functions; review properties and seed corpus before relying on randomized coverage.",
		}),
		testStrategy("benchmark", benchmarkEvidence, []string{goTestBenchmarkVerifyCommand(patterns)}, []string{
			"Detected from Benchmark* functions; run on representative hardware before capacity claims.",
		}),
		testStrategy("deterministic_simulation", simulationEvidence, []string{goTestVerifyCommand(patterns)}, []string{
			"Detected from test names or filenames containing sim, random, or property.",
		}),
		testStrategy("chaos", chaosEvidence, []string{goTestVerifyCommand(patterns)}, []string{
			"Detected from test names or filenames containing chaos or jepsen; local static context does not execute disruptive tests.",
		}),
	}
}

func matchesSimulationTestSignal(value string) bool {
	return strings.Contains(value, "sim") ||
		strings.Contains(value, "random") ||
		strings.Contains(value, "property")
}

func matchesChaosTestSignal(value string) bool {
	return strings.Contains(value, "chaos") ||
		strings.Contains(value, "jepsen")
}

func inspectCoverageSummary(rootDir string) CoverageSummary {
	summary := CoverageSummary{
		Status:            "not_found",
		Files:             []string{},
		Packages:          []string{},
		Statements:        0,
		CoveredStatements: 0,
		Percent:           0,
		Limitations: []string{
			"Coverage profiles are read only when common local Go coverage files already exist.",
			"Coverage data may be stale and is not proof of behavior-level test quality.",
			"Generated files and package ownership are not reclassified during coverage parsing.",
		},
	}

	files := coverageProfileFiles(rootDir)
	if len(files) == 0 {
		return summary
	}
	summary.Status = "detected"
	for _, file := range files {
		profile, err := parseCoverageProfile(rootDir, file)
		if err != nil {
			summary.Status = "partial"
			summary.Limitations = append(summary.Limitations, err.Error())
			continue
		}
		summary.Files = append(summary.Files, profile.File)
		if summary.Mode == "" {
			summary.Mode = profile.Mode
		} else if profile.Mode != "" && profile.Mode != summary.Mode {
			summary.Mode = "mixed"
		}
		summary.Packages = append(summary.Packages, profile.Packages...)
		summary.Statements += profile.Statements
		summary.CoveredStatements += profile.CoveredStatements
	}
	summary.Files = uniqueSortedStrings(summary.Files)
	summary.Packages = uniqueSortedStrings(summary.Packages)
	if summary.Statements > 0 {
		percent := float64(summary.CoveredStatements) / float64(summary.Statements) * 100
		summary.Percent = math.Round(percent*10) / 10
	}
	if len(summary.Files) == 0 {
		summary.Status = "not_found"
	}
	return summary
}

func coverageProfileFiles(rootDir string) []string {
	names := []string{
		"coverage.out",
		"coverage.cov",
		"cover.out",
		"cover.cov",
		"twill.cov",
	}
	files := []string{}
	for _, name := range names {
		path := filepath.Join(rootDir, name)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		files = append(files, path)
	}
	return files
}

type coverageProfile struct {
	File              string
	Mode              string
	Packages          []string
	Statements        int
	CoveredStatements int
}

func parseCoverageProfile(rootDir, filename string) (coverageProfile, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return coverageProfile{}, fmt.Errorf(
			"coverage profile %s could not be read",
			cleanPath(rootDir, filename),
		)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "mode:") {
		return coverageProfile{}, fmt.Errorf(
			"coverage profile %s is missing mode header",
			cleanPath(rootDir, filename),
		)
	}
	profile := coverageProfile{
		File:     cleanPath(rootDir, filename),
		Mode:     strings.TrimSpace(strings.TrimPrefix(lines[0], "mode:")),
		Packages: []string{},
	}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pkg, statements, count, ok := parseCoverageBlock(line)
		if !ok {
			return coverageProfile{}, fmt.Errorf("coverage profile %s contains an unrecognized block", profile.File)
		}
		profile.Packages = append(profile.Packages, pkg)
		profile.Statements += statements
		if count > 0 {
			profile.CoveredStatements += statements
		}
	}
	profile.Packages = uniqueSortedStrings(profile.Packages)
	return profile, nil
}

func parseCoverageBlock(line string) (string, int, int, bool) {
	filePart, fieldsPart, ok := strings.Cut(line, ":")
	if !ok || filePart == "" {
		return "", 0, 0, false
	}
	fields := strings.Fields(fieldsPart)
	if len(fields) != 3 {
		return "", 0, 0, false
	}
	statements, err := strconv.Atoi(fields[1])
	if err != nil || statements < 0 {
		return "", 0, 0, false
	}
	count, err := strconv.Atoi(fields[2])
	if err != nil || count < 0 {
		return "", 0, 0, false
	}
	return coveragePackage(filePart), statements, count, true
}

func coveragePackage(filePart string) string {
	slash := strings.LastIndex(filePart, "/")
	if slash < 0 {
		return "."
	}
	return filePart[:slash]
}

func testStrategy(name string, evidence []string, commands []string, notes []string) TestStrategy {
	status := "not_detected"
	if len(evidence) > 0 {
		status = "detected"
	}
	return TestStrategy{
		Name:           name,
		Status:         status,
		Evidence:       uniqueSortedStrings(evidence),
		VerifyCommands: uniqueSortedStrings(commands),
		Notes:          append([]string{}, notes...),
	}
}

func goTestVerifyCommand(patterns []string) string {
	return "go test " + verifyPatternArgs(patterns)
}

func goTestFuzzVerifyCommand(patterns []string) string {
	return "go test -run '^$' -fuzz Fuzz " + verifyPatternArgs(patterns)
}

func goTestBenchmarkVerifyCommand(patterns []string) string {
	return "go test -run '^$' -bench . -benchmem " + verifyPatternArgs(patterns)
}

func uniqueSortedStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	seen := map[string]struct{}{}
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	sort.Strings(unique)
	return unique
}
