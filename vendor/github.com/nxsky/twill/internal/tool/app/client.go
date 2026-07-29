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
	"go/token"
	"net/http"
	"strconv"
	"strings"
	"unicode"
)

const clientSDKSchemaVersion = "twill.app.client.v1"

// ClientSDKOptions configures dry-run client SDK generation.
type ClientSDKOptions struct {
	Language    string
	PackageName string
}

// ClientSDKContext describes client SDK generation context without file contents.
type ClientSDKContext struct {
	SchemaVersion     string                  `json:"schema_version"`
	Targets           []ClientSDKTarget       `json:"targets"`
	Operations        []ClientSDKOperation    `json:"operations"`
	RPCOperations     []ClientSDKRPCOperation `json:"rpc_operations"`
	Sources           []string                `json:"sources,omitempty"`
	Limitations       []string                `json:"limitations"`
	VerifyCommands    []string                `json:"verify_commands"`
	PerformedWrites   bool                    `json:"performed_writes"`
	PerformedEnvWrite bool                    `json:"performed_environment_write"`
}

// ClientSDKTarget describes one supported dry-run SDK target.
type ClientSDKTarget struct {
	Language string `json:"language"`
	Path     string `json:"path"`
}

// ClientSDKOperation describes one HTTP operation included in generated clients.
type ClientSDKOperation struct {
	OperationID  string   `json:"operation_id"`
	MethodName   string   `json:"method_name,omitempty"`
	Component    string   `json:"component"`
	Listener     string   `json:"listener"`
	Method       string   `json:"method"`
	Path         string   `json:"path"`
	PathParams   []string `json:"path_params,omitempty"`
	RequestType  string   `json:"request_type,omitempty"`
	ResponseType string   `json:"response_type,omitempty"`
	AuthDeclared bool     `json:"auth_declared,omitempty"`
	Source       string   `json:"source,omitempty"`
}

// ClientSDKRPCOperation describes one gRPC operation included in generated client planning.
type ClientSDKRPCOperation struct {
	OperationID       string `json:"operation_id"`
	MethodName        string `json:"method_name,omitempty"`
	Component         string `json:"component"`
	Listener          string `json:"listener"`
	Service           string `json:"service"`
	Method            string `json:"method"`
	Path              string `json:"path"`
	RequestType       string `json:"request_type,omitempty"`
	ResponseType      string `json:"response_type,omitempty"`
	RequestStreaming  bool   `json:"request_streaming,omitempty"`
	ResponseStreaming bool   `json:"response_streaming,omitempty"`
	Source            string `json:"source,omitempty"`
}

// ClientSDKFile is a proposed generated client file.
type ClientSDKFile struct {
	Path     string `json:"path"`
	Contents string `json:"contents"`
}

// ClientSDKPlan is the dry-run output returned by twill app client.
type ClientSDKPlan struct {
	SchemaVersion     string                  `json:"schema_version"`
	Language          string                  `json:"language"`
	PackageName       string                  `json:"package_name,omitempty"`
	Operations        []ClientSDKOperation    `json:"operations"`
	RPCOperations     []ClientSDKRPCOperation `json:"rpc_operations"`
	Files             []ClientSDKFile         `json:"files"`
	WrittenFiles      []string                `json:"written_files,omitempty"`
	Sources           []string                `json:"sources,omitempty"`
	Limitations       []string                `json:"limitations"`
	VerifyCommands    []string                `json:"verify_commands"`
	PerformedWrites   bool                    `json:"performed_writes"`
	PerformedEnvWrite bool                    `json:"performed_environment_write"`
}

// InspectClientSDK returns a dry-run client SDK plan from safe endpoint metadata.
func InspectClientSDK(ctx context.Context, opts GraphOptions, sdkOpts ClientSDKOptions) (*ClientSDKPlan, error) {
	endpoints, err := InspectEndpointsContext(ctx, opts)
	if err != nil {
		return nil, err
	}
	plan, err := ClientSDKForEndpoints(endpoints, sdkOpts)
	if err != nil {
		return nil, err
	}
	plan.VerifyCommands = clientSDKPlanVerifyCommands(opts.Patterns)
	return plan, nil
}

// ClientSDKContextForEndpoints returns safe client generation context.
func ClientSDKContextForEndpoints(endpoints EndpointsContext) ClientSDKContext {
	return clientSDKContextForEndpoints(endpoints, nil)
}

func clientSDKContextForEndpoints(endpoints EndpointsContext, patterns []string) ClientSDKContext {
	return ClientSDKContext{
		SchemaVersion: clientSDKSchemaVersion,
		Targets: []ClientSDKTarget{
			{Language: "go", Path: "client/client.go"},
			{Language: "go", Path: "client/grpc_client.go"},
			{Language: "typescript", Path: "client/twill-client.ts"},
		},
		Operations:        clientSDKOperations(endpoints, ""),
		RPCOperations:     clientSDKRPCOperations(endpoints, ""),
		Sources:           append([]string{}, endpoints.Files...),
		Limitations:       clientSDKLimitations(),
		VerifyCommands:    clientSDKContextVerifyCommands(patterns),
		PerformedWrites:   false,
		PerformedEnvWrite: false,
	}
}

// ClientSDKForEndpoints converts endpoint metadata into a dry-run client SDK plan.
func ClientSDKForEndpoints(endpoints EndpointsContext, opts ClientSDKOptions) (*ClientSDKPlan, error) {
	language, err := normalizeClientSDKLanguage(opts.Language)
	if err != nil {
		return nil, err
	}
	packageName := clientSDKPackageName(language, opts.PackageName)
	operations := clientSDKOperations(endpoints, language)
	rpcOperations := clientSDKRPCOperations(endpoints, language)

	var files []ClientSDKFile
	switch language {
	case "go":
		files = []ClientSDKFile{{
			Path:     "client/client.go",
			Contents: renderGoClientSDK(packageName, operations),
		}}
		if len(rpcOperations) > 0 {
			files = append(files, ClientSDKFile{
				Path:     "client/grpc_client.go",
				Contents: renderGoGRPCClientSDK(packageName, rpcOperations),
			})
		}
	case "typescript":
		files = []ClientSDKFile{{
			Path:     "client/twill-client.ts",
			Contents: renderTypeScriptClientSDK(operations, rpcOperations),
		}}
	}

	return &ClientSDKPlan{
		SchemaVersion:     clientSDKSchemaVersion,
		Language:          language,
		PackageName:       packageName,
		Operations:        operations,
		RPCOperations:     rpcOperations,
		Files:             files,
		Sources:           append([]string{}, endpoints.Files...),
		Limitations:       clientSDKLimitations(),
		VerifyCommands:    clientSDKPlanVerifyCommands(nil),
		PerformedWrites:   false,
		PerformedEnvWrite: false,
	}, nil
}

func clientSDKContextVerifyCommands(patterns []string) []string {
	patternArgs := verifyPatternArgs(patterns)
	return []string{
		"twill app client --lang go " + patternArgs,
		"twill app client --lang typescript " + patternArgs,
	}
}

func clientSDKPlanVerifyCommands(patterns []string) []string {
	patternArgs := verifyPatternArgs(patterns)
	return []string{
		"go test " + patternArgs,
		"twill app openapi " + patternArgs,
	}
}

func normalizeClientSDKLanguage(language string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "", "go", "golang":
		return "go", nil
	case "ts", "typescript":
		return "typescript", nil
	default:
		return "", fmt.Errorf("unsupported client SDK language %q; supported languages are go and typescript", language)
	}
}

func clientSDKPackageName(language string, packageName string) string {
	packageName = strings.TrimSpace(packageName)
	if language != "go" {
		return ""
	}
	if packageName == "" {
		return "client"
	}
	if !token.IsIdentifier(packageName) || token.Lookup(packageName).IsKeyword() {
		return "client"
	}
	return packageName
}

func clientSDKOperations(endpoints EndpointsContext, language string) []ClientSDKOperation {
	declarations := endpointOperations(endpoints)
	operations := make([]ClientSDKOperation, 0, len(declarations))
	for _, declaration := range declarations {
		method := strings.ToUpper(strings.TrimSpace(declaration.Method))
		if !supportedClientSDKMethod(method) || declaration.Path == "" {
			continue
		}
		operation := ClientSDKOperation{
			OperationID:  operationID(declaration),
			Component:    declaration.Component,
			Listener:     declaration.Listener,
			Method:       method,
			Path:         declaration.Path,
			PathParams:   pathParamNames(declaration.Path),
			RequestType:  declaration.RequestType,
			ResponseType: declaration.ResponseType,
			AuthDeclared: declaration.Auth != "",
			Source:       declaration.Source,
		}
		switch language {
		case "go":
			operation.MethodName = exportedIdentifier(operation.OperationID)
		case "typescript":
			operation.MethodName = lowerCamelIdentifier(operation.OperationID)
		}
		operations = append(operations, operation)
	}
	return operations
}

func clientSDKRPCOperations(endpoints EndpointsContext, language string) []ClientSDKRPCOperation {
	operations := []ClientSDKRPCOperation{}
	for _, declaration := range endpoints.Declarations {
		if declaration.Protocol != "grpc" {
			continue
		}
		if declaration.Service == "" || declaration.Method == "" {
			continue
		}
		operationID := rpcOperationID(declaration)
		operation := ClientSDKRPCOperation{
			OperationID:       operationID,
			Component:         declaration.Component,
			Listener:          declaration.Listener,
			Service:           declaration.Service,
			Method:            declaration.Method,
			Path:              declaration.Path,
			RequestType:       declaration.RequestType,
			ResponseType:      declaration.ResponseType,
			RequestStreaming:  declaration.RequestStreaming,
			ResponseStreaming: declaration.ResponseStreaming,
			Source:            declaration.Source,
		}
		switch language {
		case "go":
			operation.MethodName = exportedRPCIdentifier(declaration)
		case "typescript":
			operation.MethodName = lowerCamel(exportedRPCIdentifier(declaration))
		}
		operations = append(operations, operation)
	}
	return operations
}

func supportedClientSDKMethod(method string) bool {
	switch method {
	case http.MethodGet,
		http.MethodHead,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodOptions,
		http.MethodConnect,
		http.MethodTrace:
		return true
	default:
		return false
	}
}

func clientSDKLimitations() []string {
	return []string{
		"Client SDK generation is dry-run only; no files are written.",
		"gRPC adapter declarations are reported as RPC operations with stub methods; protobuf payloads and generated grpc clients remain application-owned.",
		"Request and response type references are reported when declared or inferred from matching local protobuf RPCs; HTTP body hooks are caller-owned and payload construction is not modeled yet.",
		"Generated HTTP clients validate caller-provided base URLs, expose an optional authorization header field, " +
			"per-request header/query/cancellation options, and explicit status-check plus response metadata helpers that do not read response bodies.",
		"Auth schemes, retries, base URL discovery, serialization, and production error mapping remain application-owned.",
		"Only safe endpoint metadata is used; raw request examples, response bodies, credentials, headers, and query values are not read or exposed.",
	}
}

func clientSDKWrittenLimitations() []string {
	return []string{
		"Client SDK files were written only under the requested --write-dir.",
		"Existing files with different contents are not overwritten; rerun after reviewing conflicts.",
		"gRPC adapter declarations are reported as RPC operations with stub methods; protobuf payloads and generated grpc clients remain application-owned.",
		"Request and response type references are reported when declared or inferred from matching local protobuf RPCs; HTTP body hooks are caller-owned and payload construction is not modeled yet.",
		"Generated HTTP clients validate caller-provided base URLs, expose an optional authorization header field, " +
			"per-request header/query/cancellation options, and explicit status-check plus response metadata helpers that do not read response bodies.",
		"Auth schemes, retries, base URL discovery, serialization, and production error mapping remain application-owned.",
		"Only safe endpoint metadata is used; raw request examples, response bodies, credentials, headers, and query values are not read or exposed.",
	}
}

func renderGoClientSDK(packageName string, operations []ClientSDKOperation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", packageName)
	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	b.WriteString("\t\"fmt\"\n")
	b.WriteString("\t\"io\"\n")
	b.WriteString("\t\"net/http\"\n")
	b.WriteString("\t\"net/url\"\n")
	b.WriteString("\t\"strings\"\n")
	b.WriteString(")\n\n")
	b.WriteString("type Client struct {\n")
	b.WriteString("\tBaseURL       string\n")
	b.WriteString("\tHTTPClient    *http.Client\n")
	b.WriteString("\tAuthorization string\n")
	b.WriteString("}\n\n")
	b.WriteString("func New(baseURL string) *Client {\n")
	b.WriteString("\treturn &Client{BaseURL: strings.TrimRight(baseURL, \"/\")}\n")
	b.WriteString("}\n\n")
	b.WriteString("type RequestOption func(*http.Request)\n\n")
	b.WriteString("func WithHeader(name string, value string) RequestOption {\n")
	b.WriteString("\tname = strings.TrimSpace(name)\n")
	b.WriteString("\treturn func(req *http.Request) {\n")
	b.WriteString("\t\tif name == \"\" {\n")
	b.WriteString("\t\t\treturn\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\treq.Header.Set(name, value)\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n\n")
	b.WriteString("func WithHeaders(headers http.Header) RequestOption {\n")
	b.WriteString("\treturn func(req *http.Request) {\n")
	b.WriteString("\t\tfor name, values := range headers {\n")
	b.WriteString("\t\t\tname = strings.TrimSpace(name)\n")
	b.WriteString("\t\t\tif name == \"\" {\n")
	b.WriteString("\t\t\t\tcontinue\n")
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t\treq.Header.Del(name)\n")
	b.WriteString("\t\t\tfor _, value := range values {\n")
	b.WriteString("\t\t\t\treq.Header.Add(name, value)\n")
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n\n")
	b.WriteString("func WithQueryParam(name string, value string) RequestOption {\n")
	b.WriteString("\tname = strings.TrimSpace(name)\n")
	b.WriteString("\treturn func(req *http.Request) {\n")
	b.WriteString("\t\tif name == \"\" || req.URL == nil {\n")
	b.WriteString("\t\t\treturn\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\tquery := req.URL.Query()\n")
	b.WriteString("\t\tquery.Set(name, value)\n")
	b.WriteString("\t\treq.URL.RawQuery = query.Encode()\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n\n")
	b.WriteString("func WithQueryParams(values url.Values) RequestOption {\n")
	b.WriteString("\treturn func(req *http.Request) {\n")
	b.WriteString("\t\tif req.URL == nil {\n")
	b.WriteString("\t\t\treturn\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\tquery := req.URL.Query()\n")
	b.WriteString("\t\tfor name, entries := range values {\n")
	b.WriteString("\t\t\tname = strings.TrimSpace(name)\n")
	b.WriteString("\t\t\tif name == \"\" {\n")
	b.WriteString("\t\t\t\tcontinue\n")
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t\tquery.Del(name)\n")
	b.WriteString("\t\t\tfor _, entry := range entries {\n")
	b.WriteString("\t\t\t\tquery.Add(name, entry)\n")
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\treq.URL.RawQuery = query.Encode()\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n\n")
	b.WriteString("type ResponseError struct {\n")
	b.WriteString("\tStatusCode int\n")
	b.WriteString("\tURL        string\n")
	b.WriteString("}\n\n")
	b.WriteString("func (e *ResponseError) Error() string {\n")
	b.WriteString("\tif e.URL == \"\" {\n")
	b.WriteString("\t\treturn fmt.Sprintf(\"unexpected HTTP status %d\", e.StatusCode)\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn fmt.Sprintf(\"unexpected HTTP status %d for %s\", e.StatusCode, e.URL)\n")
	b.WriteString("}\n\n")
	b.WriteString("func CheckResponse(resp *http.Response) error {\n")
	b.WriteString("\tif resp == nil {\n")
	b.WriteString("\t\treturn fmt.Errorf(\"response is nil\")\n")
	b.WriteString("\t}\n")
	b.WriteString("\tif resp.StatusCode >= 200 && resp.StatusCode < 300 {\n")
	b.WriteString("\t\treturn nil\n")
	b.WriteString("\t}\n")
	b.WriteString("\trequestURL := \"\"\n")
	b.WriteString("\tif resp.Request != nil && resp.Request.URL != nil {\n")
	b.WriteString("\t\trequestURL = resp.Request.URL.String()\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn &ResponseError{StatusCode: resp.StatusCode, URL: requestURL}\n")
	b.WriteString("}\n\n")
	b.WriteString("type ResponseMetadata struct {\n")
	b.WriteString("\tStatusCode int\n")
	b.WriteString("\tURL        string\n")
	b.WriteString("\tHeaders    http.Header\n")
	b.WriteString("}\n\n")
	b.WriteString("func MetadataFromResponse(resp *http.Response) (ResponseMetadata, error) {\n")
	b.WriteString("\tif resp == nil {\n")
	b.WriteString("\t\treturn ResponseMetadata{}, fmt.Errorf(\"response is nil\")\n")
	b.WriteString("\t}\n")
	b.WriteString("\tmetadata := ResponseMetadata{\n")
	b.WriteString("\t\tStatusCode: resp.StatusCode,\n")
	b.WriteString("\t\tHeaders:    http.Header{},\n")
	b.WriteString("\t}\n")
	b.WriteString("\tif resp.Request != nil && resp.Request.URL != nil {\n")
	b.WriteString("\t\tmetadata.URL = resp.Request.URL.String()\n")
	b.WriteString("\t}\n")
	b.WriteString("\tfor name, values := range resp.Header {\n")
	b.WriteString("\t\tcopied := make([]string, 0, len(values))\n")
	b.WriteString("\t\tmetadata.Headers[name] = append(copied, values...)\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn metadata, nil\n")
	b.WriteString("}\n\n")
	b.WriteString("func (c *Client) endpoint(path string) (string, error) {\n")
	b.WriteString("\tbaseURL := strings.TrimSpace(c.BaseURL)\n")
	b.WriteString("\tif baseURL == \"\" {\n")
	b.WriteString("\t\treturn \"\", fmt.Errorf(\"baseURL is required\")\n")
	b.WriteString("\t}\n")
	b.WriteString("\tparsed, err := url.Parse(baseURL)\n")
	b.WriteString("\tif err != nil {\n")
	b.WriteString("\t\treturn \"\", fmt.Errorf(\"parse baseURL: %w\", err)\n")
	b.WriteString("\t}\n")
	b.WriteString("\tif parsed.Scheme == \"\" || parsed.Host == \"\" {\n")
	b.WriteString("\t\treturn \"\", fmt.Errorf(\"baseURL must include scheme and host\")\n")
	b.WriteString("\t}\n")
	b.WriteString("\tif parsed.RawQuery != \"\" || parsed.Fragment != \"\" {\n")
	b.WriteString("\t\treturn \"\", fmt.Errorf(\"baseURL must not include query or fragment\")\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn strings.TrimRight(parsed.String(), \"/\") + path, nil\n")
	b.WriteString("}\n\n")
	b.WriteString("func (c *Client) do(ctx context.Context, method string, path string, body io.Reader, opts ...RequestOption) (*http.Response, error) {\n")
	b.WriteString("\thttpClient := c.HTTPClient\n")
	b.WriteString("\tif httpClient == nil {\n")
	b.WriteString("\t\thttpClient = http.DefaultClient\n")
	b.WriteString("\t}\n")
	b.WriteString("\tendpoint, err := c.endpoint(path)\n")
	b.WriteString("\tif err != nil {\n")
	b.WriteString("\t\treturn nil, err\n")
	b.WriteString("\t}\n")
	b.WriteString("\treq, err := http.NewRequestWithContext(ctx, method, endpoint, body)\n")
	b.WriteString("\tif err != nil {\n")
	b.WriteString("\t\treturn nil, err\n")
	b.WriteString("\t}\n")
	b.WriteString("\tif c.Authorization != \"\" {\n")
	b.WriteString("\t\treq.Header.Set(\"Authorization\", c.Authorization)\n")
	b.WriteString("\t}\n")
	b.WriteString("\tfor _, opt := range opts {\n")
	b.WriteString("\t\tif opt != nil {\n")
	b.WriteString("\t\t\topt(req)\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn httpClient.Do(req)\n")
	b.WriteString("}\n")
	for _, operation := range operations {
		writeGoClientSDKTypeComment(&b, operation.RequestType, operation.ResponseType)
		pathParams := goPathParamBindings(operation.PathParams)
		params := goClientSDKOperationSignature(pathParams, clientSDKOperationAcceptsBody(operation))
		fmt.Fprintf(
			&b,
			"\nfunc (c *Client) %s(ctx context.Context%s) (*http.Response, error) {\n",
			operation.MethodName,
			params,
		)
		if len(pathParams) > 0 {
			fmt.Fprintf(&b, "\tpath := %s\n", strconv.Quote(operation.Path))
			for _, param := range pathParams {
				fmt.Fprintf(
					&b,
					"\tpath = strings.ReplaceAll(path, %s, url.PathEscape(%s))\n",
					strconv.Quote("{"+param.Name+"}"),
					param.Identifier,
				)
			}
		}
		pathExpr := strconv.Quote(operation.Path)
		if len(pathParams) > 0 {
			pathExpr = "path"
		}
		fmt.Fprintf(
			&b,
			"\treturn c.do(ctx, %s, %s, %s, opts...)\n",
			goHTTPMethod(operation.Method),
			pathExpr,
			goClientSDKBodyArg(clientSDKOperationAcceptsBody(operation)),
		)
		b.WriteString("}\n")
	}
	return b.String()
}

func renderGoGRPCClientSDK(packageName string, operations []ClientSDKRPCOperation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", packageName)
	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	b.WriteString("\t\"errors\"\n")
	b.WriteString(")\n\n")
	b.WriteString("var ErrGRPCClientNotWired = errors.New(\"gRPC protobuf client is not wired\")\n")
	for _, operation := range operations {
		fmt.Fprintf(&b, "\n// %s plans %s/%s.\n", operation.MethodName, operation.Service, operation.Method)
		fmt.Fprintf(&b, "// RPC path: %s.\n", operation.Path)
		writeGoClientSDKTypeComment(&b, operation.RequestType, operation.ResponseType)
		writeGoClientSDKStreamingComment(&b, operation.RequestStreaming, operation.ResponseStreaming)
		fmt.Fprintf(&b, "\nfunc (c *Client) %s(ctx context.Context) error {\n", operation.MethodName)
		b.WriteString("\t_ = ctx\n")
		b.WriteString("\treturn ErrGRPCClientNotWired\n")
		b.WriteString("}\n")
	}
	return b.String()
}

func goHTTPMethod(method string) string {
	switch method {
	case http.MethodGet:
		return "http.MethodGet"
	case http.MethodHead:
		return "http.MethodHead"
	case http.MethodPost:
		return "http.MethodPost"
	case http.MethodPut:
		return "http.MethodPut"
	case http.MethodPatch:
		return "http.MethodPatch"
	case http.MethodDelete:
		return "http.MethodDelete"
	case http.MethodOptions:
		return "http.MethodOptions"
	case http.MethodConnect:
		return "http.MethodConnect"
	case http.MethodTrace:
		return "http.MethodTrace"
	default:
		return strconv.Quote(method)
	}
}

func renderTypeScriptClientSDK(
	operations []ClientSDKOperation,
	rpcOperations []ClientSDKRPCOperation,
) string {
	var b strings.Builder
	b.WriteString("export interface TwillClientOptions {\n")
	b.WriteString("  baseURL: string;\n")
	b.WriteString("  fetch?: typeof fetch;\n")
	b.WriteString("  authorization?: string;\n")
	b.WriteString("}\n\n")
	b.WriteString("export interface TwillRequestOptions {\n")
	b.WriteString("  headers?: Record<string, string>;\n")
	b.WriteString("  query?: Record<string, string>;\n")
	b.WriteString("  signal?: AbortSignal;\n")
	b.WriteString("}\n\n")
	b.WriteString("export interface TwillResponseMetadata {\n")
	b.WriteString("  status: number;\n")
	b.WriteString("  url: string;\n")
	b.WriteString("  headers: Record<string, string>;\n")
	b.WriteString("}\n\n")
	b.WriteString("export class TwillResponseError extends Error {\n")
	b.WriteString("  readonly status: number;\n")
	b.WriteString("  readonly url: string;\n\n")
	b.WriteString("  constructor(status: number, url: string) {\n")
	b.WriteString("    const message = url\n")
	b.WriteString("      ? `unexpected HTTP status ${status} for ${url}`\n")
	b.WriteString("      : `unexpected HTTP status ${status}`;\n")
	b.WriteString("    super(message);\n")
	b.WriteString("    this.name = \"TwillResponseError\";\n")
	b.WriteString("    this.status = status;\n")
	b.WriteString("    this.url = url;\n")
	b.WriteString("  }\n")
	b.WriteString("}\n\n")
	b.WriteString("export class TwillClient {\n")
	b.WriteString("  private readonly baseURL: string;\n")
	b.WriteString("  private readonly fetchImpl: typeof fetch;\n\n")
	b.WriteString("  private readonly authorization?: string;\n\n")
	b.WriteString("  constructor(options: TwillClientOptions) {\n")
	b.WriteString("    const baseURL = options.baseURL.trim();\n")
	b.WriteString("    if (!baseURL) {\n")
	b.WriteString("      throw new Error(\"baseURL is required\");\n")
	b.WriteString("    }\n")
	b.WriteString("    const parsedBaseURL = new URL(baseURL);\n")
	b.WriteString("    if (!parsedBaseURL.protocol || !parsedBaseURL.host) {\n")
	b.WriteString("      throw new Error(\"baseURL must include scheme and host\");\n")
	b.WriteString("    }\n")
	b.WriteString("    if (parsedBaseURL.search || parsedBaseURL.hash) {\n")
	b.WriteString("      throw new Error(\"baseURL must not include query or fragment\");\n")
	b.WriteString("    }\n")
	b.WriteString("    this.baseURL = parsedBaseURL.toString().replace(/\\/+$/, \"\");\n")
	b.WriteString("    this.fetchImpl = options.fetch ?? fetch;\n")
	b.WriteString("    this.authorization = options.authorization;\n")
	b.WriteString("  }\n\n")
	b.WriteString("  private request(method: string, path: string, body?: BodyInit, options: TwillRequestOptions = {}): Promise<Response> {\n")
	b.WriteString("    const headers: Record<string, string> = {};\n")
	b.WriteString("    if (this.authorization) {\n")
	b.WriteString("      headers.Authorization = this.authorization;\n")
	b.WriteString("    }\n")
	b.WriteString("    Object.assign(headers, options.headers ?? {});\n")
	b.WriteString("    const url = new URL(`${this.baseURL}${path}`);\n")
	b.WriteString("    for (const [name, value] of Object.entries(options.query ?? {})) {\n")
	b.WriteString("      if (name.trim()) {\n")
	b.WriteString("        url.searchParams.set(name, value);\n")
	b.WriteString("      }\n")
	b.WriteString("    }\n")
	b.WriteString("    return this.fetchImpl(url.toString(), { method, headers, body, signal: options.signal });\n")
	b.WriteString("  }\n")
	b.WriteString("\n  checkResponse(response: Response): void {\n")
	b.WriteString("    if (response.status >= 200 && response.status < 300) {\n")
	b.WriteString("      return;\n")
	b.WriteString("    }\n")
	b.WriteString("    throw new TwillResponseError(response.status, response.url);\n")
	b.WriteString("  }\n")
	b.WriteString("\n  metadataFromResponse(response: Response): TwillResponseMetadata {\n")
	b.WriteString("    const headers: Record<string, string> = {};\n")
	b.WriteString("    response.headers.forEach((value, name) => {\n")
	b.WriteString("      headers[name] = value;\n")
	b.WriteString("    });\n")
	b.WriteString("    return { status: response.status, url: response.url, headers };\n")
	b.WriteString("  }\n")
	for _, operation := range operations {
		writeTypeScriptClientSDKTypeComment(&b, operation.RequestType, operation.ResponseType)
		pathParams := typeScriptPathParamBindings(operation.PathParams)
		fmt.Fprintf(
			&b,
			"\n  %s(%s): Promise<Response> {\n",
			operation.MethodName,
			typeScriptOperationSignature(pathParams, clientSDKOperationAcceptsBody(operation)),
		)
		if len(pathParams) > 0 {
			fmt.Fprintf(&b, "    let path = %s;\n", strconv.Quote(operation.Path))
			for _, param := range pathParams {
				fmt.Fprintf(
					&b,
					"    path = path.split(%s).join(encodeURIComponent(%s));\n",
					strconv.Quote("{"+param.Name+"}"),
					param.Identifier,
				)
			}
		}
		pathExpr := strconv.Quote(operation.Path)
		if len(pathParams) > 0 {
			pathExpr = "path"
		}
		fmt.Fprintf(
			&b,
			"    return this.request(%s, %s%s);\n",
			strconv.Quote(operation.Method),
			pathExpr,
			typeScriptBodyArg(clientSDKOperationAcceptsBody(operation)),
		)
		b.WriteString("  }\n")
	}
	for _, operation := range rpcOperations {
		fmt.Fprintf(&b, "\n  // RPC path: %s.\n", operation.Path)
		writeTypeScriptClientSDKTypeComment(&b, operation.RequestType, operation.ResponseType)
		writeTypeScriptClientSDKStreamingComment(&b, operation.RequestStreaming, operation.ResponseStreaming)
		fmt.Fprintf(&b, "\n  %s(): never {\n", operation.MethodName)
		fmt.Fprintf(
			&b,
			"    throw new Error(%s);\n",
			strconv.Quote(
				"gRPC protobuf client is not wired for "+
					operation.Service+"/"+operation.Method,
			),
		)
		b.WriteString("  }\n")
	}
	b.WriteString("}\n")
	return b.String()
}

func writeGoClientSDKTypeComment(b *strings.Builder, requestType string, responseType string) {
	if requestType == "" && responseType == "" {
		return
	}
	if requestType != "" {
		fmt.Fprintf(b, "\n// Request type: %s.", requestType)
	}
	if responseType != "" {
		fmt.Fprintf(b, "\n// Response type: %s.", responseType)
	}
}

func writeGoClientSDKStreamingComment(
	b *strings.Builder,
	requestStreaming bool,
	responseStreaming bool,
) {
	if requestStreaming {
		b.WriteString("\n// Request streaming: true.")
	}
	if responseStreaming {
		b.WriteString("\n// Response streaming: true.")
	}
}

func writeTypeScriptClientSDKTypeComment(b *strings.Builder, requestType string, responseType string) {
	if requestType == "" && responseType == "" {
		return
	}
	if requestType != "" {
		fmt.Fprintf(b, "\n  // Request type: %s.", requestType)
	}
	if responseType != "" {
		fmt.Fprintf(b, "\n  // Response type: %s.", responseType)
	}
}

func writeTypeScriptClientSDKStreamingComment(
	b *strings.Builder,
	requestStreaming bool,
	responseStreaming bool,
) {
	if requestStreaming {
		b.WriteString("\n  // Request streaming: true.")
	}
	if responseStreaming {
		b.WriteString("\n  // Response streaming: true.")
	}
}

func clientSDKOperationAcceptsBody(operation ClientSDKOperation) bool {
	if strings.TrimSpace(operation.RequestType) != "" {
		return true
	}
	switch strings.ToUpper(strings.TrimSpace(operation.Method)) {
	case http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodConnect,
		http.MethodTrace:
		return true
	default:
		return false
	}
}

type pathParamBinding struct {
	Name       string
	Identifier string
}

func goClientSDKPathParamSignature(params []pathParamBinding) string {
	if len(params) == 0 {
		return ""
	}
	parts := make([]string, 0, len(params))
	for _, param := range params {
		parts = append(parts, param.Identifier+" string")
	}
	return ", " + strings.Join(parts, ", ")
}

func goClientSDKOperationSignature(params []pathParamBinding, acceptsBody bool) string {
	signature := goClientSDKPathParamSignature(params)
	if acceptsBody {
		if signature == "" {
			signature = ", body io.Reader"
		} else {
			signature += ", body io.Reader"
		}
	}
	if signature == "" {
		return ", opts ...RequestOption"
	}
	return signature + ", opts ...RequestOption"
}

func goClientSDKBodyArg(acceptsBody bool) string {
	if acceptsBody {
		return "body"
	}
	return "nil"
}

func typeScriptPathParamSignature(params []pathParamBinding) string {
	if len(params) == 0 {
		return ""
	}
	parts := make([]string, 0, len(params))
	for _, param := range params {
		parts = append(parts, param.Identifier+": string")
	}
	return strings.Join(parts, ", ")
}

func typeScriptOperationSignature(params []pathParamBinding, acceptsBody bool) string {
	signature := typeScriptPathParamSignature(params)
	if acceptsBody {
		if signature == "" {
			signature = "body: BodyInit"
		} else {
			signature += ", body: BodyInit"
		}
	}
	if signature == "" {
		return "options?: TwillRequestOptions"
	}
	return signature + ", options?: TwillRequestOptions"
}

func typeScriptBodyArg(acceptsBody bool) string {
	if acceptsBody {
		return ", body, options"
	}
	return ", undefined, options"
}

func goPathParamBindings(params []string) []pathParamBinding {
	return pathParamBindings(params, goPathParamIdentifier)
}

func typeScriptPathParamBindings(params []string) []pathParamBinding {
	return pathParamBindings(params, typeScriptPathParamIdentifier)
}

func pathParamBindings(params []string, identifier func(string) string) []pathParamBinding {
	bindings := make([]pathParamBinding, 0, len(params))
	seen := map[string]int{}
	for _, param := range params {
		base := identifier(param)
		seen[base]++
		name := base
		if seen[base] > 1 {
			name = fmt.Sprintf("%s%d", base, seen[base])
		}
		bindings = append(bindings, pathParamBinding{
			Name:       param,
			Identifier: name,
		})
	}
	return bindings
}

func goPathParamIdentifier(param string) string {
	name := lowerCamelPathParamIdentifier(param)
	if token.Lookup(name).IsKeyword() {
		return name + "Param"
	}
	return name
}

func typeScriptPathParamIdentifier(param string) string {
	name := lowerCamelPathParamIdentifier(param)
	switch name {
	case "break", "case", "catch", "class", "const", "continue", "debugger",
		"default", "delete", "do", "else", "enum", "export", "extends", "false",
		"finally", "for", "function", "if", "import", "in", "instanceof", "new",
		"null", "return", "super", "switch", "this", "throw", "true", "try",
		"typeof", "var", "void", "while", "with", "yield", "let", "static",
		"implements", "interface", "package", "private", "protected", "public":
		return name + "Param"
	default:
		return name
	}
}

func lowerCamelPathParamIdentifier(param string) string {
	parts := identifierPartsFromRunes(param)
	if len(parts) == 0 {
		return "value"
	}
	for i, part := range parts {
		part = strings.ToLower(part)
		if i == 0 {
			parts[i] = part
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, "")
}

func pathParamNames(path string) []string {
	params := []string{}
	seen := map[string]bool{}
	for {
		start := strings.Index(path, "{")
		if start < 0 {
			return params
		}
		path = path[start+1:]
		end := strings.Index(path, "}")
		if end < 0 {
			return params
		}
		param := strings.TrimSpace(path[:end])
		if param != "" && !seen[param] {
			params = append(params, param)
			seen[param] = true
		}
		path = path[end+1:]
	}
}

func identifierPartsFromRunes(value string) []string {
	parts := []string{}
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		part := b.String()
		if r := []rune(part)[0]; unicode.IsDigit(r) {
			part = "x" + part
		}
		parts = append(parts, part)
		b.Reset()
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return parts
}

func rpcOperationID(declaration EndpointDeclaration) string {
	parts := []string{
		"grpc",
		declaration.Listener,
		componentTag(declaration.Component),
		declaration.Service,
		declaration.Method,
	}
	return sanitizeOperationID(strings.Join(parts, "_"))
}

func exportedIdentifier(value string) string {
	parts := identifierParts(value)
	if len(parts) == 0 {
		return "Operation"
	}
	for i, part := range parts {
		part = strings.ToLower(part)
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	name := strings.Join(parts, "")
	if token.Lookup(name).IsKeyword() {
		return name + "Operation"
	}
	return name
}

func lowerCamelIdentifier(value string) string {
	name := exportedIdentifier(value)
	return lowerCamel(name)
}

func lowerCamel(name string) string {
	if name == "" {
		return "operation"
	}
	return strings.ToLower(name[:1]) + name[1:]
}

func exportedRPCIdentifier(declaration EndpointDeclaration) string {
	parts := []string{
		"grpc",
		declaration.Listener,
		componentTag(declaration.Component),
	}
	parts = append(parts, strings.Split(declaration.Service, ".")...)
	parts = append(parts, declaration.Method)
	var b strings.Builder
	for _, part := range parts {
		part = exportedRPCIdentifierPart(part)
		if part == "" {
			continue
		}
		b.WriteString(part)
	}
	if b.Len() == 0 {
		return "RPCOperation"
	}
	name := b.String()
	if token.Lookup(name).IsKeyword() {
		return name + "Operation"
	}
	return name
}

func exportedRPCIdentifierPart(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	part := b.String()
	if part == "" {
		return ""
	}
	runes := []rune(part)
	if unicode.IsDigit(runes[0]) {
		part = "X" + part
		runes = []rune(part)
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func identifierParts(value string) []string {
	parts := []string{}
	for _, part := range strings.Split(value, "_") {
		part = strings.TrimFunc(part, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		if part == "" {
			continue
		}
		if r := []rune(part)[0]; unicode.IsDigit(r) {
			part = "x" + part
		}
		parts = append(parts, part)
	}
	return parts
}
