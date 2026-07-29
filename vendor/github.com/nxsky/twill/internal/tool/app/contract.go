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
	"go/format"
	"net/http"
	"strconv"
	"strings"
)

const contractTestsSchemaVersion = "twill.app.contract_tests.v1"

// ContractTestsContext describes endpoint contract-test coverage without file contents.
type ContractTestsContext struct {
	SchemaVersion     string                `json:"schema_version"`
	Cases             []ContractTestCase    `json:"cases"`
	RPCCases          []RPCContractTestCase `json:"rpc_cases"`
	Targets           []ContractTestFile    `json:"targets"`
	Sources           []string              `json:"sources,omitempty"`
	Limitations       []string              `json:"limitations"`
	VerifyCommands    []string              `json:"verify_commands"`
	PerformedWrites   bool                  `json:"performed_writes"`
	PerformedEnvWrite bool                  `json:"performed_environment_write"`
}

// ContractTestCase describes one endpoint contract-test case.
type ContractTestCase struct {
	Name              string   `json:"name"`
	OperationID       string   `json:"operation_id"`
	Component         string   `json:"component"`
	Listener          string   `json:"listener"`
	Method            string   `json:"method"`
	Path              string   `json:"path"`
	PathParams        []string `json:"path_params,omitempty"`
	RequestType       string   `json:"request_type,omitempty"`
	ResponseType      string   `json:"response_type,omitempty"`
	Source            string   `json:"source,omitempty"`
	AuthDeclared      bool     `json:"auth_declared,omitempty"`
	UnsafeMethod      bool     `json:"unsafe_method,omitempty"`
	StatusEnv         string   `json:"status_env,omitempty"`
	HeadersEnv        string   `json:"headers_env,omitempty"`
	RequestHeadersEnv string   `json:"request_headers_env,omitempty"`
	QueryEnv          string   `json:"query_env,omitempty"`
	Assertions        []string `json:"assertions"`
}

// RPCContractTestCase describes one gRPC contract-test planning case.
type RPCContractTestCase struct {
	Name              string   `json:"name"`
	OperationID       string   `json:"operation_id"`
	Component         string   `json:"component"`
	Listener          string   `json:"listener"`
	Service           string   `json:"service"`
	Method            string   `json:"method"`
	Path              string   `json:"path"`
	RequestType       string   `json:"request_type,omitempty"`
	ResponseType      string   `json:"response_type,omitempty"`
	RequestStreaming  bool     `json:"request_streaming,omitempty"`
	ResponseStreaming bool     `json:"response_streaming,omitempty"`
	Source            string   `json:"source,omitempty"`
	Assertions        []string `json:"assertions"`
}

// ContractTestFile is a proposed contract-test file.
type ContractTestFile struct {
	Path     string `json:"path"`
	Contents string `json:"contents,omitempty"`
}

// ContractTestsPlan is the dry-run output returned by twill app contract-tests.
type ContractTestsPlan struct {
	SchemaVersion     string                `json:"schema_version"`
	Cases             []ContractTestCase    `json:"cases"`
	RPCCases          []RPCContractTestCase `json:"rpc_cases"`
	Files             []ContractTestFile    `json:"files"`
	WrittenFiles      []string              `json:"written_files,omitempty"`
	Sources           []string              `json:"sources,omitempty"`
	Limitations       []string              `json:"limitations"`
	VerifyCommands    []string              `json:"verify_commands"`
	PerformedWrites   bool                  `json:"performed_writes"`
	PerformedEnvWrite bool                  `json:"performed_environment_write"`
}

// InspectContractTests returns a dry-run endpoint contract-test plan.
func InspectContractTests(ctx context.Context, opts GraphOptions) (*ContractTestsPlan, error) {
	endpoints, err := InspectEndpointsContext(ctx, opts)
	if err != nil {
		return nil, err
	}
	plan := ContractTestsForEndpoints(endpoints)
	plan.VerifyCommands = contractTestsPlanVerifyCommands(opts.Patterns)
	return plan, nil
}

// ContractTestsContextForEndpoints returns safe contract-test context.
func ContractTestsContextForEndpoints(endpoints EndpointsContext) ContractTestsContext {
	return contractTestsContextForEndpoints(endpoints, nil)
}

func contractTestsContextForEndpoints(endpoints EndpointsContext, patterns []string) ContractTestsContext {
	cases := contractTestCases(endpoints)
	rpcCases := rpcContractTestCases(endpoints)
	return ContractTestsContext{
		SchemaVersion:     contractTestsSchemaVersion,
		Cases:             cases,
		RPCCases:          rpcCases,
		Targets:           contractTestTargets(rpcCases),
		Sources:           append([]string{}, endpoints.Files...),
		Limitations:       contractTestsLimitations(),
		VerifyCommands:    contractTestsContextVerifyCommands(patterns),
		PerformedWrites:   false,
		PerformedEnvWrite: false,
	}
}

// ContractTestsForEndpoints converts endpoint metadata into a dry-run contract-test plan.
func ContractTestsForEndpoints(endpoints EndpointsContext) *ContractTestsPlan {
	cases := contractTestCases(endpoints)
	rpcCases := rpcContractTestCases(endpoints)
	files := []ContractTestFile{{
		Path:     "contracttests/twill_contract_test.go",
		Contents: renderGoContractTests(cases),
	}}
	if len(rpcCases) > 0 {
		files = append(files, ContractTestFile{
			Path:     "contracttests/twill_grpc_contract_test.go",
			Contents: renderGoGRPCContractTests(rpcCases),
		})
	}
	return &ContractTestsPlan{
		SchemaVersion:     contractTestsSchemaVersion,
		Cases:             cases,
		RPCCases:          rpcCases,
		Files:             files,
		Sources:           append([]string{}, endpoints.Files...),
		Limitations:       contractTestsLimitations(),
		VerifyCommands:    contractTestsPlanVerifyCommands(nil),
		PerformedWrites:   false,
		PerformedEnvWrite: false,
	}
}

func contractTestTargets(rpcCases []RPCContractTestCase) []ContractTestFile {
	targets := []ContractTestFile{{
		Path: "contracttests/twill_contract_test.go",
	}}
	if len(rpcCases) > 0 {
		targets = append(targets, ContractTestFile{
			Path: "contracttests/twill_grpc_contract_test.go",
		})
	}
	return targets
}

func rpcContractTestCases(endpoints EndpointsContext) []RPCContractTestCase {
	cases := []RPCContractTestCase{}
	for _, declaration := range endpoints.Declarations {
		if declaration.Protocol != "grpc" {
			continue
		}
		if declaration.Service == "" || declaration.Method == "" {
			continue
		}
		operationID := rpcOperationID(declaration)
		cases = append(cases, RPCContractTestCase{
			Name:              exportedRPCIdentifier(declaration),
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
			Assertions:        rpcContractTestAssertions(declaration),
		})
	}
	return cases
}

func contractTestsContextVerifyCommands(patterns []string) []string {
	return []string{
		"twill app contract-tests " + verifyPatternArgs(patterns),
		"go test ./contracttests",
	}
}

func contractTestsPlanVerifyCommands(patterns []string) []string {
	return []string{
		"go test ./contracttests",
		"twill app openapi " + verifyPatternArgs(patterns),
	}
}

func contractTestCases(endpoints EndpointsContext) []ContractTestCase {
	declarations := endpointOperations(endpoints)
	cases := make([]ContractTestCase, 0, len(declarations))
	for _, declaration := range declarations {
		method := strings.ToUpper(strings.TrimSpace(declaration.Method))
		if !supportedContractTestMethod(method) || declaration.Path == "" {
			continue
		}
		operationID := operationID(declaration)
		cases = append(cases, ContractTestCase{
			Name:              exportedIdentifier(operationID),
			OperationID:       operationID,
			Component:         declaration.Component,
			Listener:          declaration.Listener,
			Method:            method,
			Path:              declaration.Path,
			PathParams:        pathParamNames(declaration.Path),
			RequestType:       declaration.RequestType,
			ResponseType:      declaration.ResponseType,
			Source:            declaration.Source,
			AuthDeclared:      declaration.Auth != "",
			UnsafeMethod:      unsafeContractTestMethod(method),
			StatusEnv:         contractStatusEnvName(operationID),
			HeadersEnv:        contractHeadersEnvName(operationID),
			RequestHeadersEnv: contractRequestHeadersEnvName(operationID),
			QueryEnv:          contractQueryEnvName(operationID),
			Assertions:        contractTestAssertions(declaration, method),
		})
	}
	return cases
}

func supportedContractTestMethod(method string) bool {
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

func unsafeContractTestMethod(method string) bool {
	switch method {
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

func contractTestAssertions(declaration EndpointDeclaration, method string) []string {
	assertions := []string{
		"request uses declared HTTP method",
		"request uses declared path",
		"response status is below 500",
	}
	statusEnvName := contractStatusEnvName(operationID(declaration))
	assertions = append(assertions, "expected response status may be supplied by "+statusEnvName)
	headersEnvName := contractHeadersEnvName(operationID(declaration))
	assertions = append(assertions, "expected response headers may be supplied by "+headersEnvName)
	requestHeadersEnvName := contractRequestHeadersEnvName(operationID(declaration))
	assertions = append(assertions, "request headers may be supplied by "+requestHeadersEnvName)
	queryEnvName := contractQueryEnvName(operationID(declaration))
	assertions = append(assertions, "request query parameters may be supplied by "+queryEnvName)
	if declaration.Auth != "" {
		assertions = append(assertions, "authorization must be supplied by TWILL_CONTRACT_AUTHORIZATION")
	}
	if declaration.RequestType != "" {
		assertions = append(assertions, "request type reference is "+declaration.RequestType)
	}
	if declaration.ResponseType != "" {
		assertions = append(assertions, "response type reference is "+declaration.ResponseType)
	}
	if contractTestCaseAcceptsBody(method, declaration.RequestType) {
		assertions = append(assertions, "request body may be supplied by "+contractBodyEnvName(ContractTestCase{
			OperationID: operationID(declaration),
			Method:      method,
			RequestType: declaration.RequestType,
		}))
	}
	for _, param := range contractPathParamBindings(pathParamNames(declaration.Path)) {
		assertions = append(
			assertions,
			"path parameter "+param.Name+" must be supplied by "+param.EnvName,
		)
	}
	if unsafeContractTestMethod(method) {
		assertions = append(assertions, "unsafe methods require TWILL_CONTRACT_ALLOW_UNSAFE=1")
	}
	return assertions
}

func rpcContractTestAssertions(declaration EndpointDeclaration) []string {
	assertions := []string{
		"request targets declared gRPC service",
		"request targets declared gRPC method",
		"canonical RPC path is " + declaration.Path,
		"protobuf request construction remains caller-owned",
		"non-OK status mapping must be asserted by generated protobuf clients",
	}
	if declaration.RequestType != "" {
		assertions = append(assertions, "protobuf request type reference is "+declaration.RequestType)
	}
	if declaration.ResponseType != "" {
		assertions = append(assertions, "protobuf response type reference is "+declaration.ResponseType)
	}
	if declaration.RequestStreaming {
		assertions = append(assertions, "protobuf request streaming is true")
	}
	if declaration.ResponseStreaming {
		assertions = append(assertions, "protobuf response streaming is true")
	}
	return assertions
}

func contractTestsLimitations() []string {
	return []string{
		"Contract test generation is dry-run only; no files are written.",
		"gRPC adapter declarations are reported as RPC contract cases with guarded test stubs; protobuf payload construction remains application-owned.",
		"Generated tests report declared or protobuf-inferred request and response type references; HTTP body, query, request-header, expected-status, and expected-header fixtures are caller-owned and payload construction is not modeled yet.",
		"Endpoints with declared auth require TWILL_CONTRACT_AUTHORIZATION at runtime; auth details are not generated or exposed.",
		"Unsafe HTTP methods are skipped unless TWILL_CONTRACT_ALLOW_UNSAFE=1 is set by the test runner.",
		"Templated HTTP paths require TWILL_CONTRACT_PATH_<NAME> values at runtime; path values are URL-escaped before requests are sent.",
		"Raw request examples, response bodies, credentials, headers, query values, and free-form contract text are not read or exposed.",
	}
}

func contractTestsWrittenLimitations() []string {
	return []string{
		"Contract test files were written only under the requested --write-dir.",
		"Existing files with different contents are not overwritten; rerun after reviewing conflicts.",
		"gRPC adapter declarations are reported as RPC contract cases with guarded test stubs; protobuf payload construction remains application-owned.",
		"Generated tests report declared or protobuf-inferred request and response type references; HTTP body, query, request-header, expected-status, and expected-header fixtures are caller-owned and payload construction is not modeled yet.",
		"Endpoints with declared auth require TWILL_CONTRACT_AUTHORIZATION at runtime; auth details are not generated or exposed.",
		"Unsafe HTTP methods are skipped unless TWILL_CONTRACT_ALLOW_UNSAFE=1 is set by the test runner.",
		"Templated HTTP paths require TWILL_CONTRACT_PATH_<NAME> values at runtime; path values are URL-escaped before requests are sent.",
		"Raw request examples, response bodies, credentials, headers, query values, and free-form contract text are not read or exposed.",
	}
}

func contractTestCaseAcceptsBody(method string, requestType string) bool {
	if strings.TrimSpace(requestType) != "" {
		return true
	}
	return unsafeContractTestMethod(strings.ToUpper(strings.TrimSpace(method)))
}

func contractBodyEnvName(testCase ContractTestCase) string {
	if !contractTestCaseAcceptsBody(testCase.Method, testCase.RequestType) {
		return ""
	}
	return "TWILL_CONTRACT_BODY_" + contractPathParamEnvSuffix(testCase.OperationID)
}

func contractStatusEnvName(operationID string) string {
	return "TWILL_CONTRACT_EXPECT_STATUS_" + contractPathParamEnvSuffix(operationID)
}

func contractHeadersEnvName(operationID string) string {
	return "TWILL_CONTRACT_EXPECT_HEADERS_" + contractPathParamEnvSuffix(operationID)
}

func contractRequestHeadersEnvName(operationID string) string {
	return "TWILL_CONTRACT_REQUEST_HEADERS_" + contractPathParamEnvSuffix(operationID)
}

func contractQueryEnvName(operationID string) string {
	return "TWILL_CONTRACT_QUERY_" + contractPathParamEnvSuffix(operationID)
}

func renderGoContractTests(cases []ContractTestCase) string {
	var b strings.Builder
	b.WriteString("package contracttests\n\n")
	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	b.WriteString("\t\"io\"\n")
	b.WriteString("\t\"net/http\"\n")
	b.WriteString("\t\"net/url\"\n")
	b.WriteString("\t\"os\"\n")
	b.WriteString("\t\"strconv\"\n")
	b.WriteString("\t\"strings\"\n")
	b.WriteString("\t\"testing\"\n")
	b.WriteString("\t\"time\"\n")
	b.WriteString(")\n\n")
	b.WriteString("type endpointContractCase struct {\n")
	b.WriteString("\tname         string\n")
	b.WriteString("\tmethod       string\n")
	b.WriteString("\tpath         string\n")
	b.WriteString("\tpathParams   []endpointPathParam\n")
	b.WriteString("\trequestType  string\n")
	b.WriteString("\tresponseType string\n")
	b.WriteString("\tbodyEnvName   string\n")
	b.WriteString("\tstatusEnvName string\n")
	b.WriteString("\theadersEnvName string\n")
	b.WriteString("\trequestHeadersEnvName string\n")
	b.WriteString("\tqueryEnvName   string\n")
	b.WriteString("\tauthDeclared bool\n")
	b.WriteString("\tunsafeMethod  bool\n")
	b.WriteString("}\n\n")
	b.WriteString("type endpointPathParam struct {\n")
	b.WriteString("\tname    string\n")
	b.WriteString("\tenvName string\n")
	b.WriteString("}\n\n")
	b.WriteString("var endpointContractCases = []endpointContractCase{\n")
	for _, testCase := range cases {
		fmt.Fprintf(
			&b,
			"\t{\n\t\tname: %s,\n\t\tmethod: %s,\n\t\tpath: %s,\n\t\tpathParams: %s,\n\t\trequestType: %s,\n\t\tresponseType: %s,\n\t\tbodyEnvName: %s,\n\t\tstatusEnvName: %s,\n\t\theadersEnvName: %s,\n\t\trequestHeadersEnvName: %s,\n\t\tqueryEnvName: %s,\n\t\tauthDeclared: %t,\n\t\tunsafeMethod: %t,\n\t},\n",
			strconv.Quote(testCase.Name),
			strconv.Quote(testCase.Method),
			strconv.Quote(testCase.Path),
			goContractPathParamLiteral(contractPathParamBindings(testCase.PathParams)),
			strconv.Quote(testCase.RequestType),
			strconv.Quote(testCase.ResponseType),
			strconv.Quote(contractBodyEnvName(testCase)),
			strconv.Quote(testCase.StatusEnv),
			strconv.Quote(testCase.HeadersEnv),
			strconv.Quote(testCase.RequestHeadersEnv),
			strconv.Quote(testCase.QueryEnv),
			testCase.AuthDeclared,
			testCase.UnsafeMethod,
		)
	}
	b.WriteString("}\n\n")
	b.WriteString("func TestTwillHTTPContracts(t *testing.T) {\n")
	b.WriteString("\tbaseURL, ok := contractBaseURL(t)\n")
	b.WriteString("\tif !ok {\n")
	b.WriteString("\t\treturn\n")
	b.WriteString("\t}\n")
	b.WriteString("\tauthHeader := os.Getenv(\"TWILL_CONTRACT_AUTHORIZATION\")\n")
	b.WriteString("\tallowUnsafe := os.Getenv(\"TWILL_CONTRACT_ALLOW_UNSAFE\") == \"1\"\n")
	b.WriteString("\thttpClient := &http.Client{Timeout: 10 * time.Second}\n")
	b.WriteString("\tfor _, tc := range endpointContractCases {\n")
	b.WriteString("\t\ttc := tc\n")
	b.WriteString("\t\tt.Run(tc.name, func(t *testing.T) {\n")
	b.WriteString("\t\t\tif tc.unsafeMethod && !allowUnsafe {\n")
	b.WriteString("\t\t\t\tt.Skip(\"unsafe method requires TWILL_CONTRACT_ALLOW_UNSAFE=1\")\n")
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t\tif tc.authDeclared && authHeader == \"\" {\n")
	b.WriteString("\t\t\t\tt.Skip(\"auth declared; set TWILL_CONTRACT_AUTHORIZATION\")\n")
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t\tpath, ok := contractPath(t, tc.path, tc.pathParams)\n")
	b.WriteString("\t\t\tif !ok {\n")
	b.WriteString("\t\t\t\treturn\n")
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t\tbody := contractBody(tc)\n")
	b.WriteString("\t\t\trequestURL, err := contractRequestURL(t, baseURL, path, tc)\n")
	b.WriteString("\t\t\tif err != nil {\n")
	b.WriteString("\t\t\t\tt.Fatal(err)\n")
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t\treq, err := http.NewRequestWithContext(context.Background(), tc.method, requestURL, body)\n")
	b.WriteString("\t\t\tif err != nil {\n")
	b.WriteString("\t\t\t\tt.Fatal(err)\n")
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t\tcontractRequestHeaders(t, tc, req)\n")
	b.WriteString("\t\t\tif authHeader != \"\" {\n")
	b.WriteString("\t\t\t\treq.Header.Set(\"Authorization\", authHeader)\n")
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t\tresp, err := httpClient.Do(req)\n")
	b.WriteString("\t\t\tif err != nil {\n")
	b.WriteString("\t\t\t\tt.Fatal(err)\n")
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t\tdefer resp.Body.Close()\n")
	b.WriteString("\t\t\tif expectedStatus, ok := contractExpectedStatus(t, tc); ok {\n")
	b.WriteString("\t\t\t\tif resp.StatusCode != expectedStatus {\n")
	b.WriteString("\t\t\t\t\tt.Fatalf(\"%s %s returned %d, want %d\", tc.method, tc.path, resp.StatusCode, expectedStatus)\n")
	b.WriteString("\t\t\t\t}\n")
	b.WriteString("\t\t\t} else if resp.StatusCode >= http.StatusInternalServerError {\n")
	b.WriteString("\t\t\t\tt.Fatalf(\"%s %s returned %d\", tc.method, tc.path, resp.StatusCode)\n")
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t\tcontractExpectedHeaders(t, tc, resp)\n")
	b.WriteString("\t\t})\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n")
	b.WriteString("\nfunc contractBaseURL(t *testing.T) (string, bool) {\n")
	b.WriteString("\tt.Helper()\n")
	b.WriteString("\tvalue := strings.TrimRight(strings.TrimSpace(os.Getenv(\"TWILL_CONTRACT_BASE_URL\")), \"/\")\n")
	b.WriteString("\tif value == \"\" {\n")
	b.WriteString("\t\tt.Skip(\"TWILL_CONTRACT_BASE_URL is not set\")\n")
	b.WriteString("\t\treturn \"\", false\n")
	b.WriteString("\t}\n")
	b.WriteString("\tparsed, err := url.Parse(value)\n")
	b.WriteString("\tif err != nil || parsed.Scheme == \"\" || parsed.Host == \"\" {\n")
	b.WriteString("\t\tt.Fatalf(\"TWILL_CONTRACT_BASE_URL must be an absolute http(s) URL, got %q\", value)\n")
	b.WriteString("\t}\n")
	b.WriteString("\tif parsed.Scheme != \"http\" && parsed.Scheme != \"https\" {\n")
	b.WriteString("\t\tt.Fatalf(\"TWILL_CONTRACT_BASE_URL must use http or https, got %q\", parsed.Scheme)\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn value, true\n")
	b.WriteString("}\n")
	b.WriteString("\nfunc contractPath(t *testing.T, template string, params []endpointPathParam) (string, bool) {\n")
	b.WriteString("\tt.Helper()\n")
	b.WriteString("\tpath := template\n")
	b.WriteString("\tfor _, param := range params {\n")
	b.WriteString("\t\tvalue := os.Getenv(param.envName)\n")
	b.WriteString("\t\tif value == \"\" {\n")
	b.WriteString("\t\t\tt.Skipf(\"path parameter %s requires %s\", param.name, param.envName)\n")
	b.WriteString("\t\t\treturn \"\", false\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\tpath = strings.ReplaceAll(path, \"{\"+param.name+\"}\", url.PathEscape(value))\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn path, true\n")
	b.WriteString("}\n\n")
	b.WriteString("func contractBody(tc endpointContractCase) io.Reader {\n")
	b.WriteString("\tif tc.bodyEnvName == \"\" {\n")
	b.WriteString("\t\treturn nil\n")
	b.WriteString("\t}\n")
	b.WriteString("\tvalue := os.Getenv(tc.bodyEnvName)\n")
	b.WriteString("\tif value == \"\" {\n")
	b.WriteString("\t\treturn nil\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn strings.NewReader(value)\n")
	b.WriteString("}\n\n")
	b.WriteString("func contractRequestURL(t *testing.T, baseURL string, path string, tc endpointContractCase) (string, error) {\n")
	b.WriteString("\tt.Helper()\n")
	b.WriteString("\tparsed, err := url.Parse(baseURL + path)\n")
	b.WriteString("\tif err != nil {\n")
	b.WriteString("\t\treturn \"\", err\n")
	b.WriteString("\t}\n")
	b.WriteString("\tvalue := strings.TrimSpace(os.Getenv(tc.queryEnvName))\n")
	b.WriteString("\tif value == \"\" {\n")
	b.WriteString("\t\treturn parsed.String(), nil\n")
	b.WriteString("\t}\n")
	b.WriteString("\tquery := parsed.Query()\n")
	b.WriteString("\tfor _, entry := range contractKeyValueEntries(value) {\n")
	b.WriteString("\t\tname, rawValue, ok := strings.Cut(entry, \"=\")\n")
	b.WriteString("\t\tname = strings.TrimSpace(name)\n")
	b.WriteString("\t\tif !ok || name == \"\" {\n")
	b.WriteString("\t\t\tt.Fatalf(\"%s must contain query=value entries\", tc.queryEnvName)\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\tquery.Add(name, strings.TrimSpace(rawValue))\n")
	b.WriteString("\t}\n")
	b.WriteString("\tparsed.RawQuery = query.Encode()\n")
	b.WriteString("\treturn parsed.String(), nil\n")
	b.WriteString("}\n\n")
	b.WriteString("func contractRequestHeaders(t *testing.T, tc endpointContractCase, req *http.Request) {\n")
	b.WriteString("\tt.Helper()\n")
	b.WriteString("\tif tc.requestHeadersEnvName == \"\" {\n")
	b.WriteString("\t\treturn\n")
	b.WriteString("\t}\n")
	b.WriteString("\tvalue := strings.TrimSpace(os.Getenv(tc.requestHeadersEnvName))\n")
	b.WriteString("\tif value == \"\" {\n")
	b.WriteString("\t\treturn\n")
	b.WriteString("\t}\n")
	b.WriteString("\tfor _, entry := range contractKeyValueEntries(value) {\n")
	b.WriteString("\t\tname, rawValue, ok := strings.Cut(entry, \"=\")\n")
	b.WriteString("\t\tname = strings.TrimSpace(name)\n")
	b.WriteString("\t\tif !ok || name == \"\" {\n")
	b.WriteString("\t\t\tt.Fatalf(\"%s must contain header=value entries\", tc.requestHeadersEnvName)\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\treq.Header.Add(name, strings.TrimSpace(rawValue))\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n\n")
	b.WriteString("func contractExpectedStatus(t *testing.T, tc endpointContractCase) (int, bool) {\n")
	b.WriteString("\tt.Helper()\n")
	b.WriteString("\tif tc.statusEnvName == \"\" {\n")
	b.WriteString("\t\treturn 0, false\n")
	b.WriteString("\t}\n")
	b.WriteString("\tvalue := strings.TrimSpace(os.Getenv(tc.statusEnvName))\n")
	b.WriteString("\tif value == \"\" {\n")
	b.WriteString("\t\treturn 0, false\n")
	b.WriteString("\t}\n")
	b.WriteString("\tstatus, err := strconv.Atoi(value)\n")
	b.WriteString("\tif err != nil || status < 100 || status > 599 {\n")
	b.WriteString("\t\tt.Fatalf(\"%s must be an HTTP status code from 100 through 599\", tc.statusEnvName)\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn status, true\n")
	b.WriteString("}\n\n")
	b.WriteString("func contractExpectedHeaders(t *testing.T, tc endpointContractCase, resp *http.Response) {\n")
	b.WriteString("\tt.Helper()\n")
	b.WriteString("\tif tc.headersEnvName == \"\" {\n")
	b.WriteString("\t\treturn\n")
	b.WriteString("\t}\n")
	b.WriteString("\tvalue := strings.TrimSpace(os.Getenv(tc.headersEnvName))\n")
	b.WriteString("\tif value == \"\" {\n")
	b.WriteString("\t\treturn\n")
	b.WriteString("\t}\n")
	b.WriteString("\tfor _, expectation := range contractKeyValueEntries(value) {\n")
	b.WriteString("\t\tname, want, ok := strings.Cut(expectation, \"=\")\n")
	b.WriteString("\t\tname = strings.TrimSpace(name)\n")
	b.WriteString("\t\twant = strings.TrimSpace(want)\n")
	b.WriteString("\t\tif !ok || name == \"\" {\n")
	b.WriteString("\t\t\tt.Fatalf(\"%s must contain header=value entries\", tc.headersEnvName)\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t\tif got := resp.Header.Get(name); got != want {\n")
	b.WriteString("\t\t\tt.Fatalf(\"%s %s response header %s = %q, want %q\", tc.method, tc.path, name, got, want)\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n\n")
	b.WriteString("func contractKeyValueEntries(value string) []string {\n")
	b.WriteString("\tvalue = strings.ReplaceAll(value, \",\", \"\\n\")\n")
	b.WriteString("\tparts := strings.Split(value, \"\\n\")\n")
	b.WriteString("\tout := make([]string, 0, len(parts))\n")
	b.WriteString("\tfor _, part := range parts {\n")
	b.WriteString("\t\tpart = strings.TrimSpace(part)\n")
	b.WriteString("\t\tif part != \"\" {\n")
	b.WriteString("\t\t\tout = append(out, part)\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn out\n")
	b.WriteString("}\n\n")
	return formatGeneratedGoSource(b.String())
}

func renderGoGRPCContractTests(cases []RPCContractTestCase) string {
	var b strings.Builder
	b.WriteString("package contracttests\n\n")
	b.WriteString("import (\n")
	b.WriteString("\t\"os\"\n")
	b.WriteString("\t\"testing\"\n")
	b.WriteString(")\n\n")
	b.WriteString("type grpcContractCase struct {\n")
	b.WriteString("\tname         string\n")
	b.WriteString("\tservice      string\n")
	b.WriteString("\tmethod       string\n")
	b.WriteString("\tpath         string\n")
	b.WriteString("\trequestType  string\n")
	b.WriteString("\tresponseType string\n")
	b.WriteString("\trequestStreaming  bool\n")
	b.WriteString("\tresponseStreaming bool\n")
	b.WriteString("}\n\n")
	b.WriteString("var grpcContractCases = []grpcContractCase{\n")
	for _, testCase := range cases {
		fmt.Fprintf(
			&b,
			"\t{\n\t\tname: %s,\n\t\tservice: %s,\n\t\tmethod: %s,\n\t\tpath: %s,\n\t\trequestType: %s,\n\t\tresponseType: %s,\n\t\trequestStreaming: %t,\n\t\tresponseStreaming: %t,\n\t},\n",
			strconv.Quote(testCase.Name),
			strconv.Quote(testCase.Service),
			strconv.Quote(testCase.Method),
			strconv.Quote(testCase.Path),
			strconv.Quote(testCase.RequestType),
			strconv.Quote(testCase.ResponseType),
			testCase.RequestStreaming,
			testCase.ResponseStreaming,
		)
	}
	b.WriteString("}\n\n")
	b.WriteString("func TestTwillGRPCContracts(t *testing.T) {\n")
	b.WriteString("\ttarget := os.Getenv(\"TWILL_GRPC_CONTRACT_TARGET\")\n")
	b.WriteString("\tif target == \"\" {\n")
	b.WriteString("\t\tt.Skip(\"TWILL_GRPC_CONTRACT_TARGET is not set\")\n")
	b.WriteString("\t}\n")
	b.WriteString("\tfor _, tc := range grpcContractCases {\n")
	b.WriteString("\t\ttc := tc\n")
	b.WriteString("\t\tt.Run(tc.name, func(t *testing.T) {\n")
	b.WriteString("\t\t\tt.Skip(\"wire generated protobuf client and request payload for \" + tc.path)\n")
	b.WriteString("\t\t})\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n")
	return formatGeneratedGoSource(b.String())
}

func formatGeneratedGoSource(contents string) string {
	formatted, err := format.Source([]byte(contents))
	if err != nil {
		return contents
	}
	return string(formatted)
}

type contractPathParamBinding struct {
	Name    string
	EnvName string
}

func contractPathParamBindings(params []string) []contractPathParamBinding {
	bindings := make([]contractPathParamBinding, 0, len(params))
	seen := map[string]int{}
	for _, param := range params {
		base := "TWILL_CONTRACT_PATH_" + contractPathParamEnvSuffix(param)
		seen[base]++
		envName := base
		if seen[base] > 1 {
			envName = fmt.Sprintf("%s_%d", base, seen[base])
		}
		bindings = append(bindings, contractPathParamBinding{
			Name:    param,
			EnvName: envName,
		})
	}
	return bindings
}

func goContractPathParamLiteral(params []contractPathParamBinding) string {
	if len(params) == 0 {
		return "nil"
	}
	parts := make([]string, 0, len(params))
	for _, param := range params {
		parts = append(
			parts,
			fmt.Sprintf(
				"{name: %s, envName: %s}",
				strconv.Quote(param.Name),
				strconv.Quote(param.EnvName),
			),
		)
	}
	return "[]endpointPathParam{" + strings.Join(parts, ", ") + "}"
}

func contractPathParamEnvSuffix(param string) string {
	var b strings.Builder
	for _, r := range param {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "VALUE"
	}
	return b.String()
}
