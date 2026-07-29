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
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const openAPISchemaVersion = "twill.openapi.v1"

// OpenAPIValidation reports coverage of endpoint contracts in the generated
// OpenAPI document. It is populated when validation is requested.
type OpenAPIValidation struct {
	Valid            bool     `json:"valid"`
	MissingContracts []string `json:"missing_contracts,omitempty"`
	ExtraOperations  []string `json:"extra_operations,omitempty"`
	Mismatches       []string `json:"mismatches,omitempty"`
}

// OpenAPIDocument is the deterministic OpenAPI export emitted by twill app openapi.
type OpenAPIDocument struct {
	SchemaVersion string                      `json:"schema_version"`
	OpenAPI       string                      `json:"openapi"`
	Info          OpenAPIInfo                 `json:"info"`
	Paths         map[string]OpenAPIPathItem  `json:"paths"`
	Components    *OpenAPIComponents          `json:"components,omitempty"`
	Tags          []OpenAPITag                `json:"tags,omitempty"`
	Limitations   []string                    `json:"x-twill-limitations"`
	Sources       []string                    `json:"x-twill-sources,omitempty"`
	Extensions    map[string]OpenAPIExtension `json:"x-twill-extensions,omitempty"`
	Validation    *OpenAPIValidation          `json:"x-twill-validation,omitempty"`
}

// OpenAPIInfo describes the exported API document.
type OpenAPIInfo struct {
	Title   string `json:"title"`
	Version string `json:"version"`
}

// OpenAPIPathItem describes operations for one OpenAPI path.
type OpenAPIPathItem map[string]OpenAPIOperation

// OpenAPIOperation describes a safe endpoint operation summary.
type OpenAPIOperation struct {
	OperationID string                     `json:"operationId"`
	Tags        []string                   `json:"tags,omitempty"`
	Summary     string                     `json:"summary"`
	Parameters  []OpenAPIParameter         `json:"parameters,omitempty"`
	RequestBody *OpenAPIRequestBody        `json:"requestBody,omitempty"`
	Security    []OpenAPISecurity          `json:"security,omitempty"`
	Responses   map[string]OpenAPIResponse `json:"responses"`
	Extensions  OpenAPIExtension           `json:"x-twill"`
}

// OpenAPIParameter describes a generated OpenAPI operation parameter.
type OpenAPIParameter struct {
	Name     string        `json:"name"`
	In       string        `json:"in"`
	Required bool          `json:"required"`
	Schema   OpenAPISchema `json:"schema"`
}

// OpenAPISchema describes the schema metadata Twill can safely export.
// Items, AdditionalProperties, and Properties use any to avoid recursive
// type cycles that would break MCP SDK schema reflection.
type OpenAPISchema struct {
	Type                 string         `json:"type,omitempty"`
	Format               string         `json:"format,omitempty"`
	Ref                  string         `json:"$ref,omitempty"`
	Items                any            `json:"items,omitempty"`
	AdditionalProperties any            `json:"additionalProperties,omitempty"`
	Properties           map[string]any `json:"properties,omitempty"`
	Required             []string       `json:"required,omitempty"`
	TwillTypeRef         string         `json:"x-twill-type-ref,omitempty"`
	SchemaStatus         string         `json:"x-twill-schema-status,omitempty"`
}

// OpenAPIResponse describes a generated OpenAPI response.
type OpenAPIResponse struct {
	Description string                      `json:"description"`
	Content     map[string]OpenAPIMediaType `json:"content,omitempty"`
}

// OpenAPIRequestBody describes a generated OpenAPI request body.
type OpenAPIRequestBody struct {
	Required bool                        `json:"required"`
	Content  map[string]OpenAPIMediaType `json:"content"`
}

// OpenAPIMediaType describes a generated OpenAPI media type.
type OpenAPIMediaType struct {
	Schema OpenAPISchema `json:"schema"`
}

// OpenAPIComponents describes reusable OpenAPI component metadata.
type OpenAPIComponents struct {
	SecuritySchemes map[string]OpenAPISecurityScheme `json:"securitySchemes,omitempty"`
	Schemas         map[string]OpenAPISchema         `json:"schemas,omitempty"`
}

// OpenAPISecurityScheme describes a standard OpenAPI security scheme.
type OpenAPISecurityScheme struct {
	Type string `json:"type"`
	In   string `json:"in,omitempty"`
	Name string `json:"name,omitempty"`
}

// OpenAPISecurity describes operation-level security requirements.
type OpenAPISecurity map[string][]string

// OpenAPITag describes a component tag.
type OpenAPITag struct {
	Name string `json:"name"`
}

// OpenAPIExtension carries Twill-specific safe metadata.
type OpenAPIExtension struct {
	Component     string `json:"component,omitempty"`
	Listener      string `json:"listener,omitempty"`
	RequestType   string `json:"request_type,omitempty"`
	ResponseType  string `json:"response_type,omitempty"`
	Auth          string `json:"auth,omitempty"`
	Middleware    string `json:"middleware,omitempty"`
	Compatibility string `json:"compatibility,omitempty"`
	Source        string `json:"source,omitempty"`
}

// InspectOpenAPI exports a minimal OpenAPI document from safe endpoint context.
func InspectOpenAPI(ctx context.Context, opts GraphOptions) (*OpenAPIDocument, error) {
	endpoints, err := InspectEndpointsContext(ctx, opts)
	if err != nil {
		return nil, err
	}
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}
	return OpenAPIForEndpointsWithSchemaEnrichment(endpoints, dir), nil
}

// InspectOpenAPIWithValidation exports an OpenAPI document and validates it
// against the endpoint contracts that contributed to it.
func InspectOpenAPIWithValidation(ctx context.Context, opts GraphOptions) (*OpenAPIDocument, error) {
	endpoints, err := InspectEndpointsContext(ctx, opts)
	if err != nil {
		return nil, err
	}
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}
	doc := OpenAPIForEndpointsWithSchemaEnrichment(endpoints, dir)
	doc.Validation = ValidateOpenAPIDocument(doc, endpoints)
	return doc, nil
}

// ValidateOpenAPIDocument checks that every endpoint contract is represented in
// the generated OpenAPI document and that every OpenAPI operation sourced from a
// contract has a matching contract. It returns nil when no contracts exist.
func ValidateOpenAPIDocument(doc *OpenAPIDocument, endpoints EndpointsContext) *OpenAPIValidation {
	if len(endpoints.Contracts) == 0 {
		return nil
	}
	validation := &OpenAPIValidation{Valid: true}

	contractKeys := map[string]EndpointContract{}
	for _, contract := range endpoints.Contracts {
		key := openAPIOperationKey(contract.Method, contract.Path)
		contractKeys[key] = contract
	}

	operationKeys := map[string]bool{}
	for path, item := range doc.Paths {
		for method := range item {
			key := openAPIOperationKey(strings.ToUpper(method), path)
			operationKeys[key] = true
		}
	}

	for key, contract := range contractKeys {
		if !operationKeys[key] {
			validation.MissingContracts = append(validation.MissingContracts, fmt.Sprintf(
				"%s %s (component %s, listener %s)",
				contract.Method,
				contract.Path,
				contract.Component,
				contract.Listener,
			))
		}
	}
	for key := range operationKeys {
		if _, ok := contractKeys[key]; !ok {
			validation.ExtraOperations = append(validation.ExtraOperations, key)
		}
	}

	if len(validation.MissingContracts) > 0 || len(validation.ExtraOperations) > 0 {
		validation.Valid = false
	}
	sort.Strings(validation.MissingContracts)
	sort.Strings(validation.ExtraOperations)
	return validation
}

func openAPIOperationKey(method, path string) string {
	return strings.ToUpper(method) + " " + path
}

// OpenAPIForEndpoints converts safe endpoint contracts to deterministic OpenAPI.
func OpenAPIForEndpoints(endpoints EndpointsContext) *OpenAPIDocument {
	return openAPIForEndpointsWithIntrospector(endpoints, nil)
}

// OpenAPIForEndpointsWithSchemaEnrichment converts endpoint contracts to
// OpenAPI with field-level schema enrichment from Go type introspection.
func OpenAPIForEndpointsWithSchemaEnrichment(endpoints EndpointsContext, dir string) *OpenAPIDocument {
	introspector := newTypeIntrospector(dir)
	refs := collectTypeRefs(endpoints.Declarations)
	if len(refs) > 0 {
		pkgPaths := pkgPathsFromRefs(refs)
		patterns := resolvePkgPatterns(dir, pkgPaths)
		if err := introspector.loadPackages(patterns); err != nil {
			// Fall back to non-enriched schemas on error.
			return openAPIForEndpointsWithIntrospector(endpoints, nil)
		}
	}
	return openAPIForEndpointsWithIntrospector(endpoints, introspector)
}

func openAPIForEndpointsWithIntrospector(endpoints EndpointsContext, introspector *typeIntrospector) *OpenAPIDocument {
	doc := &OpenAPIDocument{
		SchemaVersion: openAPISchemaVersion,
		OpenAPI:       "3.1.0",
		Info: OpenAPIInfo{
			Title:   "Twill Application API",
			Version: "0.0.0",
		},
		Paths:       map[string]OpenAPIPathItem{},
		Tags:        []OpenAPITag{},
		Limitations: openAPILimitations(),
		Sources:     []string{},
	}

	tagSet := map[string]struct{}{}
	sourceSet := map[string]struct{}{}
	schemaNames := map[string]string{}
	usedSchemaNames := map[string]int{}
	for _, declaration := range endpointOperations(endpoints) {
		method := strings.ToLower(declaration.Method)
		if method == "" || declaration.Path == "" {
			continue
		}
		source := openAPISource(declaration.Source)
		if doc.Paths[declaration.Path] == nil {
			doc.Paths[declaration.Path] = OpenAPIPathItem{}
		}
		tag := componentTag(declaration.Component)
		requestBody := openAPIRequestBody(doc, declaration.RequestType, schemaNames, usedSchemaNames, introspector)
		doc.Paths[declaration.Path][method] = OpenAPIOperation{
			OperationID: operationID(declaration),
			Tags:        []string{tag},
			Summary:     fmt.Sprintf("%s %s", strings.ToUpper(method), declaration.Path),
			Parameters:  openAPIPathParameters(declaration.Path),
			RequestBody: requestBody,
			Security:    openAPIOperationSecurity(declaration),
			Responses:   openAPIResponses(doc, declaration.ResponseType, schemaNames, usedSchemaNames, introspector),
			Extensions: OpenAPIExtension{
				Component:     declaration.Component,
				Listener:      declaration.Listener,
				RequestType:   declaration.RequestType,
				ResponseType:  declaration.ResponseType,
				Auth:          declaration.Auth,
				Middleware:    declaration.Middleware,
				Compatibility: declaration.Compatibility,
				Source:        source,
			},
		}
		tagSet[tag] = struct{}{}
		if declaration.Auth != "" {
			openAPIAddAuthorization(doc)
		}
		if source != "" {
			sourceSet[source] = struct{}{}
		}
	}

	for tag := range tagSet {
		doc.Tags = append(doc.Tags, OpenAPITag{Name: tag})
	}
	sort.Slice(doc.Tags, func(i, j int) bool {
		return doc.Tags[i].Name < doc.Tags[j].Name
	})
	for source := range sourceSet {
		doc.Sources = append(doc.Sources, source)
	}
	sort.Strings(doc.Sources)
	return doc
}

func openAPISource(source string) string {
	if filepath.Base(source) == "twill_gen.go" {
		return ""
	}
	return source
}

func openAPIPathParameters(path string) []OpenAPIParameter {
	params := pathParamNames(path)
	if len(params) == 0 {
		return nil
	}
	parameters := make([]OpenAPIParameter, 0, len(params))
	for _, param := range params {
		parameters = append(parameters, OpenAPIParameter{
			Name:     param,
			In:       "path",
			Required: true,
			Schema: OpenAPISchema{
				Type: "string",
			},
		})
	}
	return parameters
}

func openAPIOperationSecurity(declaration EndpointDeclaration) []OpenAPISecurity {
	if declaration.Auth == "" {
		return nil
	}
	return []OpenAPISecurity{{
		"twillAuthorization": []string{},
	}}
}

func openAPIRequestBody(
	doc *OpenAPIDocument,
	requestType string,
	schemaNames map[string]string,
	usedNames map[string]int,
	introspector *typeIntrospector,
) *OpenAPIRequestBody {
	schema := openAPITypeRefSchema(doc, requestType, schemaNames, usedNames, introspector)
	if schema.Ref == "" {
		return nil
	}
	return &OpenAPIRequestBody{
		Required: false,
		Content: map[string]OpenAPIMediaType{
			"application/json": {
				Schema: schema,
			},
		},
	}
}

func openAPIResponses(
	doc *OpenAPIDocument,
	responseType string,
	schemaNames map[string]string,
	usedNames map[string]int,
	introspector *typeIntrospector,
) map[string]OpenAPIResponse {
	schema := openAPITypeRefSchema(doc, responseType, schemaNames, usedNames, introspector)
	if schema.Ref == "" {
		return map[string]OpenAPIResponse{
			"default": {
				Description: "Response schema is not modeled yet.",
			},
		}
	}
	return map[string]OpenAPIResponse{
		"200": {
			Description: "OK",
			Content: map[string]OpenAPIMediaType{
				"application/json": {
					Schema: schema,
				},
			},
		},
	}
}

func openAPITypeRefSchema(
	doc *OpenAPIDocument,
	typeRef string,
	schemaNames map[string]string,
	usedNames map[string]int,
	introspector *typeIntrospector,
) OpenAPISchema {
	typeRef = strings.TrimSpace(typeRef)
	if typeRef == "" {
		return OpenAPISchema{}
	}
	name, ok := schemaNames[typeRef]
	if !ok {
		name = openAPITypeSchemaName(typeRef, usedNames)
		schemaNames[typeRef] = name
		components := ensureOpenAPIComponents(doc)
		if components.Schemas == nil {
			components.Schemas = map[string]OpenAPISchema{}
		}
		// Try to enrich the schema with field-level properties.
		schema := OpenAPISchema{
			Type:         "object",
			TwillTypeRef: typeRef,
			SchemaStatus: "type_reference_only",
		}
		if introspector != nil {
			if enriched := enrichSchema(introspector, typeRef); enriched != nil {
				schema = *enriched
				schema.TwillTypeRef = typeRef
			}
		}
		components.Schemas[name] = schema
	}
	return OpenAPISchema{Ref: "#/components/schemas/" + name}
}

// enrichSchema introspects the Go type referenced by typeRef and returns
// a schema with field-level properties, or nil if the type cannot be found.
func enrichSchema(introspector *typeIntrospector, typeRef string) *OpenAPISchema {
	fields := introspector.introspectFields(typeRef)
	if fields == nil {
		return nil
	}
	schema := &OpenAPISchema{
		Type: "object",
	}
	if len(fields) > 0 {
		schema.Properties = make(map[string]any, len(fields))
		for _, f := range fields {
			schema.Properties[f.Name] = f.Schema
			if f.Required {
				schema.Required = append(schema.Required, f.Name)
			}
		}
		sort.Strings(schema.Required)
	}
	return schema
}

func openAPITypeSchemaName(typeRef string, usedNames map[string]int) string {
	parts := identifierPartsFromRunes(typeRef)
	if len(parts) == 0 {
		parts = []string{"TwillType"}
	}
	base := strings.Join(parts, "_")
	usedNames[base]++
	if usedNames[base] == 1 {
		return base
	}
	return fmt.Sprintf("%s_%d", base, usedNames[base])
}

func openAPIAddAuthorization(doc *OpenAPIDocument) {
	components := ensureOpenAPIComponents(doc)
	if components.SecuritySchemes == nil {
		components.SecuritySchemes = map[string]OpenAPISecurityScheme{}
	}
	components.SecuritySchemes["twillAuthorization"] = OpenAPISecurityScheme{
		Type: "apiKey",
		In:   "header",
		Name: "Authorization",
	}
}

func ensureOpenAPIComponents(doc *OpenAPIDocument) *OpenAPIComponents {
	if doc.Components == nil {
		doc.Components = &OpenAPIComponents{}
	}
	return doc.Components
}

func openAPILimitations() []string {
	return []string{
		"OpenAPI export is generated from safe HTTP endpoint declarations and contract summaries when present.",
		"Only method, path, path parameters, declared authorization headers, component, listener, source file, safe type references, and normalized endpoint declaration metadata are exported.",
		"Request and response schemas are enriched from Go struct fields when type information is available via go/packages; fields, auth scheme details, middleware internals, examples, non-authorization headers, query values, and bodies beyond struct fields are not exposed.",
	}
}

func componentTag(component string) string {
	if component == "" {
		return "unknown"
	}
	parts := strings.Split(component, "/")
	return parts[len(parts)-1]
}

func endpointOperations(endpoints EndpointsContext) []EndpointDeclaration {
	if len(endpoints.Declarations) > 0 {
		operations := make([]EndpointDeclaration, 0, len(endpoints.Declarations))
		for _, declaration := range endpoints.Declarations {
			if !isOpenAPIEndpointDeclaration(declaration) {
				continue
			}
			operations = append(operations, declaration)
		}
		return operations
	}
	operations := make([]EndpointDeclaration, 0, len(endpoints.Contracts))
	for _, contract := range endpoints.Contracts {
		operations = append(operations, EndpointDeclaration{
			Component: contract.Component,
			Listener:  contract.Listener,
			Method:    contract.Method,
			Path:      contract.Path,
			Source:    contract.Source,
		})
	}
	return operations
}

func isOpenAPIEndpointDeclaration(declaration EndpointDeclaration) bool {
	if declaration.Protocol == "" || declaration.Protocol == "http" {
		return true
	}
	return false
}

func operationID(declaration EndpointDeclaration) string {
	parts := []string{
		declaration.Method,
		declaration.Listener,
		componentTag(declaration.Component),
		declaration.Path,
	}
	return sanitizeOperationID(strings.Join(parts, "_"))
}

var operationIDPattern = regexp.MustCompile(`[^A-Za-z0-9]+`)

func sanitizeOperationID(value string) string {
	value = operationIDPattern.ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "operation"
	}
	return value
}
