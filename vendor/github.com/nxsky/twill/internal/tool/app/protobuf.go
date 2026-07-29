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
)

const (
	protobufSchemaVersion = "twill.app.protobuf.v1"
	maxProtoFileBytes     = 256 * 1024
)

// ProtobufContext describes safe protobuf contract metadata.
type ProtobufContext struct {
	SchemaVersion string                `json:"schema_version"`
	Packages      []ProtobufPackage     `json:"packages"`
	Services      []ProtobufService     `json:"services"`
	Messages      []ProtobufMessage     `json:"messages"`
	RuntimeHints  []ProtobufRuntimeHint `json:"runtime_hints"`
	Files         []string              `json:"files,omitempty"`
	Limitations   []string              `json:"limitations"`
}

// ProtobufPackage describes one protobuf package declaration.
type ProtobufPackage struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// ProtobufService describes one protobuf service declaration.
type ProtobufService struct {
	Name    string        `json:"name"`
	Package string        `json:"package,omitempty"`
	RPCs    []ProtobufRPC `json:"rpcs"`
	Source  string        `json:"source"`
}

// ProtobufRPC describes one protobuf RPC declaration.
type ProtobufRPC struct {
	Name              string `json:"name"`
	RequestType       string `json:"request_type,omitempty"`
	ResponseType      string `json:"response_type,omitempty"`
	RequestStreaming  bool   `json:"request_streaming,omitempty"`
	ResponseStreaming bool   `json:"response_streaming,omitempty"`
}

// ProtobufMessage describes one protobuf message declaration.
type ProtobufMessage struct {
	Name    string `json:"name"`
	Package string `json:"package,omitempty"`
	Source  string `json:"source"`
}

// ProtobufRuntimeHint describes available runtime and generated-code integration surfaces.
type ProtobufRuntimeHint struct {
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Evidence []string `json:"evidence"`
	NextStep string   `json:"next_step"`
}

// InspectProtobufContext inspects local .proto files and returns safe contract metadata.
func InspectProtobufContext(ctx context.Context, opts GraphOptions) (ProtobufContext, error) {
	_ = ctx
	rootDir, err := inspectionRootDir(opts)
	if err != nil {
		return ProtobufContext{}, err
	}

	files := []string{}
	err = filepath.WalkDir(rootDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if skipProtoDir(entry.Name()) && path != rootDir {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".proto" {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return ProtobufContext{}, fmt.Errorf("walk protobuf files %s: %w", rootDir, err)
	}
	sort.Strings(files)

	context := ProtobufContext{
		SchemaVersion: protobufSchemaVersion,
		Packages:      []ProtobufPackage{},
		Services:      []ProtobufService{},
		Messages:      []ProtobufMessage{},
		RuntimeHints:  protobufRuntimeHints(),
		Files:         []string{},
		Limitations:   protobufLimitations(),
	}
	for _, path := range files {
		fileContext, err := inspectProtobufFile(rootDir, path)
		if err != nil {
			return ProtobufContext{}, err
		}
		context.Packages = append(context.Packages, fileContext.Packages...)
		context.Services = append(context.Services, fileContext.Services...)
		context.Messages = append(context.Messages, fileContext.Messages...)
		context.Files = append(context.Files, fileContext.Files...)
	}
	sortProtobufContext(&context)
	return context, nil
}

func inspectProtobufFile(rootDir string, path string) (ProtobufContext, error) {
	info, err := os.Stat(path)
	if err != nil {
		return ProtobufContext{}, fmt.Errorf("stat protobuf file %s: %w", path, err)
	}
	if info.Size() > maxProtoFileBytes {
		return ProtobufContext{}, fmt.Errorf(
			"protobuf file %s is %d bytes, maximum is %d bytes",
			path,
			info.Size(),
			maxProtoFileBytes,
		)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ProtobufContext{}, fmt.Errorf("read protobuf file %s: %w", path, err)
	}
	source := cleanPath(rootDir, path)
	return protobufContextFromSource(source, string(data)), nil
}

func protobufContextFromSource(source string, contents string) ProtobufContext {
	contents = stripProtoBlockComments(contents)
	pkg := ""
	context := ProtobufContext{
		SchemaVersion: protobufSchemaVersion,
		Packages:      []ProtobufPackage{},
		Services:      []ProtobufService{},
		Messages:      []ProtobufMessage{},
		RuntimeHints:  protobufRuntimeHints(),
		Files:         []string{source},
		Limitations:   protobufLimitations(),
	}
	var current *ProtobufService
	serviceDepth := 0
	rpcDecl := ""
	for _, rawLine := range strings.Split(contents, "\n") {
		line := stripProtoLineComment(rawLine)
		if line == "" {
			continue
		}
		if current != nil {
			if rpcDecl != "" || strings.HasPrefix(line, "rpc ") {
				rpcDecl = strings.TrimSpace(rpcDecl + " " + line)
			}
			if rpcDecl != "" && protobufRPCDeclarationComplete(rpcDecl) {
				if rpc := protobufRPC(rpcDecl); rpc.Name != "" {
					current.RPCs = append(current.RPCs, rpc)
				}
				rpcDecl = ""
			} else if rpcDecl == "" {
				if rpc := protobufRPC(line); rpc.Name != "" {
					current.RPCs = append(current.RPCs, rpc)
				}
			}
			if rpcDecl == "" {
				serviceDepth += strings.Count(line, "{")
				serviceDepth -= strings.Count(line, "}")
			}
			if serviceDepth <= 0 {
				context.Services = append(context.Services, *current)
				current = nil
			}
			continue
		}
		if name := protobufPackageName(line); name != "" {
			pkg = name
			context.Packages = append(context.Packages, ProtobufPackage{
				Name:   name,
				Source: source,
			})
			continue
		}
		if name := protobufMessageName(line); name != "" {
			context.Messages = append(context.Messages, ProtobufMessage{
				Name:    name,
				Package: pkg,
				Source:  source,
			})
			continue
		}
		if name := protobufServiceName(line); name != "" {
			serviceDepth = strings.Count(line, "{") - strings.Count(line, "}")
			current = &ProtobufService{
				Name:    name,
				Package: pkg,
				RPCs:    []ProtobufRPC{},
				Source:  source,
			}
			if serviceDepth <= 0 {
				context.Services = append(context.Services, *current)
				current = nil
			}
		}
	}
	if current != nil {
		if rpc := protobufRPC(rpcDecl); rpc.Name != "" {
			current.RPCs = append(current.RPCs, rpc)
		}
		context.Services = append(context.Services, *current)
	}
	sortProtobufContext(&context)
	return context
}

func skipProtoDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "vendor", "node_modules", ".twill":
		return true
	default:
		return false
	}
}

func stripProtoBlockComments(contents string) string {
	pattern := regexp.MustCompile(`(?s)/\*.*?\*/`)
	return pattern.ReplaceAllString(contents, "")
}

func stripProtoLineComment(line string) string {
	if before, _, ok := strings.Cut(line, "//"); ok {
		line = before
	}
	return strings.TrimSpace(line)
}

var (
	protobufPackagePattern = regexp.MustCompile(`^package\s+([A-Za-z_][A-Za-z0-9_.]*)\s*;`)
	protobufServicePattern = regexp.MustCompile(`^service\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{?`)
	protobufMessagePattern = regexp.MustCompile(`^message\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{?`)
	protobufRPCPattern     = regexp.MustCompile(`^rpc\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(\s*(stream\s+)?([A-Za-z_][A-Za-z0-9_.]*)\s*\)\s+returns\s*\(\s*(stream\s+)?([A-Za-z_][A-Za-z0-9_.]*)\s*\)`)
)

func protobufPackageName(line string) string {
	matches := protobufPackagePattern.FindStringSubmatch(line)
	if len(matches) != 2 {
		return ""
	}
	return matches[1]
}

func protobufServiceName(line string) string {
	matches := protobufServicePattern.FindStringSubmatch(line)
	if len(matches) != 2 {
		return ""
	}
	return matches[1]
}

func protobufMessageName(line string) string {
	matches := protobufMessagePattern.FindStringSubmatch(line)
	if len(matches) != 2 {
		return ""
	}
	return matches[1]
}

func protobufRPC(line string) ProtobufRPC {
	matches := protobufRPCPattern.FindStringSubmatch(line)
	if len(matches) != 6 {
		return ProtobufRPC{}
	}
	return ProtobufRPC{
		Name:              matches[1],
		RequestType:       matches[3],
		ResponseType:      matches[5],
		RequestStreaming:  strings.TrimSpace(matches[2]) != "",
		ResponseStreaming: strings.TrimSpace(matches[4]) != "",
	}
}

func protobufRPCDeclarationComplete(line string) bool {
	return strings.Contains(line, ";") || strings.Contains(line, "{")
}

func sortProtobufContext(context *ProtobufContext) {
	sort.Slice(context.Packages, func(i, j int) bool {
		if context.Packages[i].Name != context.Packages[j].Name {
			return context.Packages[i].Name < context.Packages[j].Name
		}
		return context.Packages[i].Source < context.Packages[j].Source
	})
	sort.Slice(context.Services, func(i, j int) bool {
		if context.Services[i].Package != context.Services[j].Package {
			return context.Services[i].Package < context.Services[j].Package
		}
		if context.Services[i].Name != context.Services[j].Name {
			return context.Services[i].Name < context.Services[j].Name
		}
		return context.Services[i].Source < context.Services[j].Source
	})
	for i := range context.Services {
		sort.Slice(context.Services[i].RPCs, func(j, k int) bool {
			return context.Services[i].RPCs[j].Name < context.Services[i].RPCs[k].Name
		})
	}
	sort.Slice(context.Messages, func(i, j int) bool {
		if context.Messages[i].Package != context.Messages[j].Package {
			return context.Messages[i].Package < context.Messages[j].Package
		}
		if context.Messages[i].Name != context.Messages[j].Name {
			return context.Messages[i].Name < context.Messages[j].Name
		}
		return context.Messages[i].Source < context.Messages[j].Source
	})
	sort.Strings(context.Files)
}

func protobufLimitations() []string {
	return []string{
		"Protobuf context reports package, service, RPC, request/response streaming markers, message type names, and source files only.",
		"Message fields, enum values, options, comments, examples, payloads, and custom annotations are not exposed.",
		"Parsing is a conservative source scan, not a full protoc-compatible parser.",
		"Runtime gRPC adapter helpers, client stubs, and contract-test stubs are available; protobuf payload construction remains application-owned.",
	}
}

func protobufRuntimeHints() []ProtobufRuntimeHint {
	return []ProtobufRuntimeHint{
		{
			Name:   "grpc_adapter_runtime",
			Status: "available",
			Evidence: []string{
				"runtime/adapters.MountGRPCServices",
				"runtime/adapters.ServeGRPC",
			},
			NextStep: "Register generated protobuf services with adapters.GRPCBinding and serve them on the Twill listener.",
		},
		{
			Name:   "client_sdk_rpc_stubs",
			Status: "stubbed",
			Evidence: []string{
				"twill app client rpc_operations",
				"ErrGRPCClientNotWired",
			},
			NextStep: "Wire generated protobuf clients and request payload types in application-owned client packages.",
		},
		{
			Name:   "contract_test_rpc_stubs",
			Status: "stubbed",
			Evidence: []string{
				"twill app contract-tests rpc_cases",
				"TWILL_GRPC_CONTRACT_TARGET",
			},
			NextStep: "Wire generated protobuf clients and fixtures before enabling gRPC contract tests.",
		},
	}
}
