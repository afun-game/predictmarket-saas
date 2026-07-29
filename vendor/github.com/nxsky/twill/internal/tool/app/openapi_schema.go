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
	"fmt"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/tools/go/packages"
)

// schemaField describes a single field in a JSON schema object.
type schemaField struct {
	Name     string
	Required bool
	Schema   OpenAPISchema
}

// typeIntrospector loads Go packages with type information and introspects
// struct types to generate JSON Schema field definitions.
type typeIntrospector struct {
	dir     string
	mu      sync.Mutex
	loaded  bool
	pkgs    []*packages.Package
	typeMap map[string]*types.Struct // "pkgpath.Name" -> struct type
}

func newTypeIntrospector(dir string) *typeIntrospector {
	return &typeIntrospector{
		dir:     dir,
		typeMap: map[string]*types.Struct{},
	}
}

// loadPackages loads Go packages with type information for the given
// directory and patterns. It is called lazily on first schema lookup.
func (ti *typeIntrospector) loadPackages(patterns []string) error {
	ti.mu.Lock()
	defer ti.mu.Unlock()
	if ti.loaded {
		return nil
	}
	ti.loaded = true

	cfg := &packages.Config{
		Dir:  ti.dir,
		Mode: packages.NeedTypes | packages.NeedTypesInfo | packages.NeedSyntax | packages.NeedName,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return fmt.Errorf("load packages for schema enrichment: %w", err)
	}
	ti.pkgs = pkgs
	for _, pkg := range pkgs {
		if pkg.Types == nil {
			continue
		}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			named, ok := obj.Type().(*types.Named)
			if !ok {
				continue
			}
			s, ok := named.Underlying().(*types.Struct)
			if !ok {
				continue
			}
			key := pkg.PkgPath + "." + name
			ti.typeMap[key] = s
		}
	}
	return nil
}

// resolveTypeRef normalizes a type reference string to "pkgpath.Name" form.
func resolveTypeRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	// Already in "pkgpath.Name" form.
	if !strings.Contains(ref, "/") || strings.HasPrefix(ref, "github.com/") {
		return ref
	}
	// Try to resolve relative to common patterns.
	return ref
}

// lookupStruct finds the struct type for the given type reference.
func (ti *typeIntrospector) lookupStruct(ref string) *types.Struct {
	ref = resolveTypeRef(ref)
	if s, ok := ti.typeMap[ref]; ok {
		return s
	}
	return nil
}

// introspectFields returns schema fields for the named type reference.
// Returns nil if the type cannot be found or is not a struct.
func (ti *typeIntrospector) introspectFields(ref string) []schemaField {
	s := ti.lookupStruct(ref)
	if s == nil {
		return nil
	}
	return structFields(s)
}

// structFields extracts schema fields from a go/types.Struct.
func structFields(s *types.Struct) []schemaField {
	var fields []schemaField
	for i := 0; i < s.NumFields(); i++ {
		f := s.Field(i)
		if !f.Exported() {
			continue
		}
		tag := s.Tag(i)
		jsonName, omitempty, skip := parseJSONTag(tag, f.Name())
		if skip {
			continue
		}
		fields = append(fields, schemaField{
			Name:     jsonName,
			Required: !omitempty,
			Schema:   goTypeToSchema(f.Type()),
		})
	}
	return fields
}

// parseJSONTag extracts the JSON field name and options from a struct tag.
func parseJSONTag(tag, fieldName string) (name string, omitempty bool, skip bool) {
	name = fieldName
	if tag == "" {
		return name, false, false
	}
	jsonTag := getStructTagValue(tag, "json")
	if jsonTag == "-" {
		return "", false, true
	}
	if jsonTag == "" {
		return name, false, false
	}
	parts := strings.Split(jsonTag, ",")
	if parts[0] != "" {
		name = parts[0]
	}
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty, false
}

// getStructTagValue extracts the value for key from a raw struct tag string.
func getStructTagValue(tag, key string) string {
	// Use strconv.Unquote to get the raw tag content, then parse.
	// Simple parsing: look for `key:"value"` pattern.
	pattern := key + ":"
	idx := strings.Index(tag, pattern)
	if idx < 0 {
		return ""
	}
	start := idx + len(pattern)
	if start >= len(tag) || tag[start] != '"' {
		return ""
	}
	end := strings.Index(tag[start+1:], `"`)
	if end < 0 {
		return ""
	}
	return tag[start+1 : start+1+end]
}

// goTypeToSchema maps a Go type to an OpenAPI schema.
func goTypeToSchema(t types.Type) OpenAPISchema {
	switch v := t.(type) {
	case *types.Basic:
		return basicTypeToSchema(v)
	case *types.Pointer:
		return goTypeToSchema(v.Elem())
	case *types.Named:
		return namedTypeToSchema(v)
	case *types.Slice:
		return sliceTypeToSchema(v)
	case *types.Array:
		return OpenAPISchema{
			Type:  "array",
			Items: goTypeToSchema(v.Elem()),
		}
	case *types.Map:
		return mapTypeToSchema(v)
	case *types.Interface:
		return OpenAPISchema{Type: "object", SchemaStatus: "interface"}
	default:
		return OpenAPISchema{Type: "object", SchemaStatus: "unsupported"}
	}
}

func basicTypeToSchema(b *types.Basic) OpenAPISchema {
	switch b.Kind() {
	case types.String:
		return OpenAPISchema{Type: "string"}
	case types.Bool:
		return OpenAPISchema{Type: "boolean"}
	case types.Int, types.Int32:
		return OpenAPISchema{Type: "integer", Format: "int32"}
	case types.Int64:
		return OpenAPISchema{Type: "integer", Format: "int64"}
	case types.Float32:
		return OpenAPISchema{Type: "number", Format: "float"}
	case types.Float64:
		return OpenAPISchema{Type: "number", Format: "double"}
	case types.Uint, types.Uint32:
		return OpenAPISchema{Type: "integer", Format: "int32"}
	case types.Uint64:
		return OpenAPISchema{Type: "integer", Format: "int64"}
	case types.Byte:
		return OpenAPISchema{Type: "string", Format: "byte"}
	default:
		return OpenAPISchema{Type: "string"}
	}
}

func namedTypeToSchema(n *types.Named) OpenAPISchema {
	// Check for well-known types by name.
	fullName := n.Obj().Pkg().Path() + "." + n.Obj().Name()
	switch fullName {
	case "time.Time":
		return OpenAPISchema{Type: "string", Format: "date-time"}
	case "time.Duration":
		return OpenAPISchema{Type: "integer", Format: "int64"}
	}
	// Check underlying type.
	switch u := n.Underlying().(type) {
	case *types.Basic:
		return basicTypeToSchema(u)
	case *types.Struct:
		// Named struct: reference it as a component schema.
		return OpenAPISchema{Ref: "#/components/schemas/" + schemaNameForNamed(n)}
	case *types.Slice:
		return sliceTypeToSchema(u)
	case *types.Array:
		return OpenAPISchema{
			Type:  "array",
			Items: goTypeToSchema(u.Elem()),
		}
	case *types.Map:
		return mapTypeToSchema(u)
	default:
		return OpenAPISchema{Type: "object", SchemaStatus: "unsupported"}
	}
}

func sliceTypeToSchema(s *types.Slice) OpenAPISchema {
	return OpenAPISchema{
		Type:  "array",
		Items: goTypeToSchema(s.Elem()),
	}
}

func mapTypeToSchema(m *types.Map) OpenAPISchema {
	// Only support string-keyed maps.
	if b, ok := m.Key().(*types.Basic); !ok || b.Kind() != types.String {
		return OpenAPISchema{Type: "object", SchemaStatus: "non_string_map"}
	}
	return OpenAPISchema{
		Type:                 "object",
		AdditionalProperties: goTypeToSchema(m.Elem()),
	}
}

func schemaNameForNamed(n *types.Named) string {
	pkgPath := n.Obj().Pkg().Path()
	name := n.Obj().Name()
	parts := strings.Split(pkgPath, "/")
	last := parts[len(parts)-1]
	return last + "_" + name
}

// collectTypeRefs returns all unique type references from endpoint declarations.
func collectTypeRefs(declarations []EndpointDeclaration) []string {
	seen := map[string]struct{}{}
	for _, d := range declarations {
		if d.RequestType != "" {
			seen[resolveTypeRef(d.RequestType)] = struct{}{}
		}
		if d.ResponseType != "" {
			seen[resolveTypeRef(d.ResponseType)] = struct{}{}
		}
	}
	refs := make([]string, 0, len(seen))
	for ref := range seen {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}

// pkgPathsFromRefs extracts unique package paths from type references.
func pkgPathsFromRefs(refs []string) []string {
	seen := map[string]struct{}{}
	for _, ref := range refs {
		idx := strings.LastIndex(ref, ".")
		if idx < 0 {
			continue
		}
		pkgPath := ref[:idx]
		seen[pkgPath] = struct{}{}
	}
	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// resolvePkgPatterns converts package paths to patterns suitable for
// packages.Load, relative to the inspection directory.
func resolvePkgPatterns(dir string, pkgPaths []string) []string {
	var patterns []string
	for _, p := range pkgPaths {
		// Try absolute path first.
		abs := filepath.Join(dir, "go.sum")
		_ = abs
		patterns = append(patterns, p)
	}
	return patterns
}
