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
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/nxsky/twill/runtime/codegen"
	"golang.org/x/tools/go/packages"
)

const graphSchemaVersion = "twill.app.graph.v1"

// GraphSchemaVersion is the schema version string for exported app graphs.
const GraphSchemaVersion = graphSchemaVersion

// Graph describes the static Twill application graph extracted from generated
// metadata.
type Graph struct {
	SchemaVersion     string      `json:"schema_version"`
	Packages          []Package   `json:"packages"`
	Components        []Component `json:"components"`
	Edges             []Edge      `json:"edges"`
	GeneratedFiles    []string    `json:"generated_files"`
	Limitations       []string    `json:"limitations"`
	VerifyCommands    []string    `json:"verify_commands"`
	WrittenFiles      []string    `json:"written_files,omitempty"`
	PerformedWrites   bool        `json:"performed_writes"`
	PerformedEnvWrite bool        `json:"performed_environment_write"`
}

// Package describes a Go package scanned for Twill metadata.
type Package struct {
	Path string `json:"path"`
	Dir  string `json:"dir"`
}

// Component describes a Twill component discovered in generated metadata.
type Component struct {
	Name      string   `json:"name"`
	Package   string   `json:"package"`
	Listeners []string `json:"listeners,omitempty"`
}

// Edge describes a component dependency edge.
type Edge struct {
	Caller string `json:"caller"`
	Callee string `json:"callee"`
}

// GraphOptions configures graph inspection.
type GraphOptions struct {
	Dir      string
	Patterns []string
}

func packageLoadDir(opts GraphOptions) string {
	if opts.Dir == "" {
		return "."
	}
	return opts.Dir
}

func inspectionRootDir(opts GraphOptions) (string, error) {
	dir := packageLoadDir(opts)
	rootDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve app root %s: %w", dir, err)
	}
	if len(opts.Patterns) != 1 {
		return rootDir, nil
	}
	pattern := opts.Patterns[0]
	if pattern == "" || pattern == "." || strings.Contains(pattern, "...") {
		return rootDir, nil
	}
	candidate := pattern
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(rootDir, filepath.FromSlash(pattern))
	}
	info, err := os.Stat(candidate)
	if err != nil || !info.IsDir() {
		return rootDir, nil
	}
	return filepath.Abs(candidate)
}

// InspectGraph inspects packages and returns their Twill application graph.
func InspectGraph(ctx context.Context, opts GraphOptions) (*Graph, error) {
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
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}

	inspector := graphInspector{
		rootDir:       rootDir,
		packages:      map[string]Package{},
		componentPkgs: map[string]string{},
		listeners:     map[string][]string{},
		edges:         map[Edge]struct{}{},
		generated:     map[string]struct{}{},
	}
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			return nil, packageErrors(pkg)
		}
		if err := inspector.inspectPackage(pkg); err != nil {
			return nil, err
		}
	}
	return inspector.graph(), nil
}

type graphInspector struct {
	rootDir       string
	packages      map[string]Package
	componentPkgs map[string]string
	listeners     map[string][]string
	edges         map[Edge]struct{}
	generated     map[string]struct{}
}

func (g *graphInspector) inspectPackage(pkg *packages.Package) error {
	if pkg.PkgPath == "" {
		return nil
	}

	pkgDir := cleanPath(g.rootDir, pkg.Dir)
	g.packages[pkg.PkgPath] = Package{
		Path: pkg.PkgPath,
		Dir:  pkgDir,
	}

	for _, filename := range pkg.GoFiles {
		if filepath.Base(filename) != "twill_gen.go" {
			continue
		}
		if err := g.inspectGeneratedFile(pkg.PkgPath, filename); err != nil {
			return err
		}
	}
	return nil
}

func (g *graphInspector) inspectGeneratedFile(pkgPath, filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("read generated file %s: %w", filename, err)
	}
	g.generated[cleanPath(g.rootDir, filename)] = struct{}{}

	for _, component := range extractComponentNames(data) {
		g.componentPkgs[component] = pkgPath
	}
	for _, edge := range codegen.ExtractEdges(data) {
		g.edges[Edge{Caller: edge[0], Callee: edge[1]}] = struct{}{}
		if _, ok := g.componentPkgs[edge[0]]; !ok {
			g.componentPkgs[edge[0]] = componentPackage(edge[0])
		}
		if _, ok := g.componentPkgs[edge[1]]; !ok {
			g.componentPkgs[edge[1]] = componentPackage(edge[1])
		}
	}
	for _, lis := range codegen.ExtractListeners(data) {
		listeners := append([]string{}, lis.Listeners...)
		sort.Strings(listeners)
		g.listeners[lis.Component] = listeners
		if _, ok := g.componentPkgs[lis.Component]; !ok {
			g.componentPkgs[lis.Component] = componentPackage(lis.Component)
		}
	}
	return nil
}

func (g *graphInspector) graph() *Graph {
	graph := &Graph{
		SchemaVersion:  graphSchemaVersion,
		Packages:       []Package{},
		Components:     []Component{},
		Edges:          []Edge{},
		GeneratedFiles: []string{},
	}

	for _, pkg := range g.packages {
		graph.Packages = append(graph.Packages, pkg)
	}
	sort.Slice(graph.Packages, func(i, j int) bool {
		return graph.Packages[i].Path < graph.Packages[j].Path
	})

	for name, pkg := range g.componentPkgs {
		var listeners []string
		if componentListeners, ok := g.listeners[name]; ok {
			listeners = append([]string{}, componentListeners...)
		}
		graph.Components = append(graph.Components, Component{
			Name:      name,
			Package:   pkg,
			Listeners: listeners,
		})
	}
	sort.Slice(graph.Components, func(i, j int) bool {
		return graph.Components[i].Name < graph.Components[j].Name
	})

	for edge := range g.edges {
		graph.Edges = append(graph.Edges, edge)
	}
	sort.Slice(graph.Edges, func(i, j int) bool {
		if graph.Edges[i].Caller != graph.Edges[j].Caller {
			return graph.Edges[i].Caller < graph.Edges[j].Caller
		}
		return graph.Edges[i].Callee < graph.Edges[j].Callee
	})

	for filename := range g.generated {
		graph.GeneratedFiles = append(graph.GeneratedFiles, filename)
	}
	sort.Strings(graph.GeneratedFiles)

	graph.Limitations = graphLimitations()
	graph.VerifyCommands = graphVerifyCommands()

	return graph
}

func graphLimitations() []string {
	return []string{
		"Graph is extracted from generated twill_gen.go metadata and package structure only.",
		"Dynamic component creation, runtime wiring, and conditional registrations are not captured.",
		"Source-level call graphs and non-Twill dependencies are not included.",
	}
}

func graphVerifyCommands() []string {
	return []string{
		"twill app graph ./...",
		"twill app components ./...",
		"twill app endpoints ./...",
	}
}

var componentNameRE = regexp.MustCompile(`Name:\s*"([^"]+)"`)

func extractComponentNames(data []byte) []string {
	matches := componentNameRE.FindAllSubmatch(data, -1)
	seen := map[string]struct{}{}
	var names []string
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		name := string(match[1])
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func componentPackage(component string) string {
	if i := strings.LastIndex(component, "/"); i >= 0 {
		return component[:i]
	}
	return ""
}

func cleanPath(root, path string) string {
	if path == "" {
		return ""
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.Clean(path)
	}
	return filepath.ToSlash(rel)
}

func packageErrors(pkg *packages.Package) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s:", pkg.PkgPath)
	for _, err := range pkg.Errors {
		fmt.Fprintf(&b, "\n  %s", err)
	}
	return fmt.Errorf("%s", b.String())
}
