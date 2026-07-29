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
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
)

type generatedFile struct {
	Path     string
	Contents string
}

// WriteClientSDKPlan writes generated client SDK files under writeDir.
func WriteClientSDKPlan(plan *ClientSDKPlan, writeDir string) error {
	files := make([]generatedFile, 0, len(plan.Files))
	for _, file := range plan.Files {
		files = append(files, generatedFile(file))
	}
	written, contents, err := writeGeneratedFiles(writeDir, files)
	if err != nil {
		return err
	}
	for i, file := range plan.Files {
		if contents, ok := contents[file.Path]; ok {
			plan.Files[i].Contents = contents
		}
	}
	plan.WrittenFiles = written
	plan.PerformedWrites = len(written) > 0
	plan.Limitations = clientSDKWrittenLimitations()
	return nil
}

// WriteContractTestsPlan writes generated contract-test files under writeDir.
func WriteContractTestsPlan(plan *ContractTestsPlan, writeDir string) error {
	files := make([]generatedFile, 0, len(plan.Files))
	for _, file := range plan.Files {
		files = append(files, generatedFile(file))
	}
	written, contents, err := writeGeneratedFiles(writeDir, files)
	if err != nil {
		return err
	}
	for i, file := range plan.Files {
		if contents, ok := contents[file.Path]; ok {
			plan.Files[i].Contents = contents
		}
	}
	plan.WrittenFiles = written
	plan.PerformedWrites = len(written) > 0
	plan.Limitations = contractTestsWrittenLimitations()
	return nil
}

// WriteLocalComposePlan writes generated Docker Compose files under writeDir.
func WriteLocalComposePlan(plan *LocalComposePlan, writeDir string) error {
	files := make([]generatedFile, 0, len(plan.Files))
	for _, file := range plan.Files {
		files = append(files, generatedFile(file))
	}
	written, contents, err := writeGeneratedFiles(writeDir, files)
	if err != nil {
		return err
	}
	for i, file := range plan.Files {
		if contents, ok := contents[file.Path]; ok {
			plan.Files[i].Contents = contents
		}
	}
	plan.WrittenFiles = written
	plan.PerformedWrites = len(written) > 0
	plan.Limitations = localComposeWrittenLimitations()
	return nil
}

func writeGraph(graph *Graph, writeDir string) error {
	data, err := json.MarshalIndent(graph, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal graph: %w", err)
	}
	written, _, err := writeGeneratedFiles(writeDir, []generatedFile{{
		Path:     "app-graph.json",
		Contents: string(data),
	}})
	if err != nil {
		return err
	}
	graph.WrittenFiles = written
	graph.PerformedWrites = len(written) > 0
	graph.Limitations = graphWrittenLimitations()
	return nil
}

func graphWrittenLimitations() []string {
	return []string{
		"Application graph was written only under the requested --write-dir.",
		"Existing files with different contents are not overwritten; rerun after reviewing conflicts.",
		"The graph contains only static metadata from generated files and package structure.",
	}
}

func writeGeneratedFiles(writeDir string, files []generatedFile) ([]string, map[string]string, error) {
	writeDir = strings.TrimSpace(writeDir)
	if writeDir == "" {
		return nil, nil, nil
	}
	root, err := filepath.Abs(writeDir)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve write dir %q: %w", writeDir, err)
	}
	prepared := make([]generatedFileWrite, 0, len(files))
	contentsByPath := make(map[string]string, len(files))
	seen := map[string]struct{}{}
	for _, file := range files {
		target, err := generatedFileTarget(root, file.Path)
		if err != nil {
			return nil, nil, err
		}
		if _, ok := seen[target]; ok {
			return nil, nil, fmt.Errorf("generated file target %q is duplicated", file.Path)
		}
		seen[target] = struct{}{}
		contents := []byte(file.Contents)
		if strings.HasSuffix(file.Path, ".go") {
			formatted, err := format.Source(contents)
			if err != nil {
				return nil, nil, fmt.Errorf("format generated Go file %s: %w", file.Path, err)
			}
			contents = formatted
		}
		relPath := filepath.ToSlash(filepath.Clean(file.Path))
		contentsByPath[relPath] = string(contents)
		prepared = append(prepared, generatedFileWrite{
			relPath:  relPath,
			target:   target,
			contents: contents,
		})
	}
	for i := range prepared {
		existing, err := os.ReadFile(prepared[i].target)
		if err == nil {
			if !bytes.Equal(existing, prepared[i].contents) {
				return nil, nil, fmt.Errorf("refusing to overwrite %s with different generated contents", prepared[i].target)
			}
			prepared[i].existsIdentical = true
			continue
		}
		if !os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("read generated file target %s: %w", prepared[i].target, err)
		}
	}
	written := make([]string, 0, len(prepared))
	for _, file := range prepared {
		if file.existsIdentical {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(file.target), 0755); err != nil {
			return nil, nil, fmt.Errorf("create generated file directory %s: %w", filepath.Dir(file.target), err)
		}
		if err := os.WriteFile(file.target, file.contents, 0644); err != nil {
			return nil, nil, fmt.Errorf("write generated file %s: %w", file.target, err)
		}
		written = append(written, file.relPath)
	}
	return written, contentsByPath, nil
}

type generatedFileWrite struct {
	relPath         string
	target          string
	contents        []byte
	existsIdentical bool
}

func generatedFileTarget(root string, relPath string) (string, error) {
	if strings.TrimSpace(relPath) == "" {
		return "", fmt.Errorf("generated file path is empty")
	}
	if strings.Contains(relPath, "\\") || strings.Contains(relPath, ":") {
		return "", fmt.Errorf("generated file path %q must use relative slash-separated paths", relPath)
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("generated file path %q must be relative", relPath)
	}
	clean := filepath.Clean(relPath)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("generated file path %q escapes write dir", relPath)
	}
	return filepath.Join(root, clean), nil
}
