package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	gort "runtime"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/config"
)

func TestToolDescriptorsExposeSafetyAnnotations(t *testing.T) {
	descriptors := toolDescriptorsForConfig(t, []string{"read_file", "skill_package", "task_manage"}, config.Config{})
	byName := map[string]map[string]any{}
	for _, descriptor := range descriptors {
		name, _ := descriptor["name"].(string)
		byName[name] = descriptor
	}

	assertToolAnnotation(t, byName["read_file"], true, false, false)
	assertToolAnnotation(t, byName["skill_package"], false, true, true)
	assertToolAnnotation(t, byName["task_manage"], false, false, false)

	for _, def := range app.ToolDefinitions() {
		if def.Annotations == nil {
			t.Fatalf("%s has no safety annotations", def.Name)
		}
	}
}

func assertToolAnnotation(t *testing.T, descriptor map[string]any, readOnly, destructive, openWorld bool) {
	t.Helper()
	annotations, ok := descriptor["annotations"].(map[string]any)
	if !ok {
		t.Fatalf("descriptor has no annotations: %#v", descriptor)
	}
	if got, _ := annotations["readOnlyHint"].(bool); got != readOnly {
		t.Fatalf("readOnlyHint = %v, want %v", got, readOnly)
	}
	assertBoolPointer := func(key string, want bool) {
		value, ok := annotations[key].(*bool)
		if !ok || value == nil || *value != want {
			t.Fatalf("%s = %#v, want %v", key, annotations[key], want)
		}
	}
	assertBoolPointer("destructiveHint", destructive)
	assertBoolPointer("openWorldHint", openWorld)
}

func TestOpenAIFileMetadataMatchesDeclaredSchemas(t *testing.T) {
	for _, def := range app.ToolDefinitions() {
		meta := toolMetadata(def, true)
		inputProps, _ := def.InputSchema["properties"].(map[string]any)
		for _, path := range def.FileArgRewritePaths {
			property, ok := inputProps[path].(map[string]any)
			if !ok {
				t.Fatalf("%s file input path %q missing from input schema", def.Name, path)
			}
			if property["type"] != "string" || property["format"] != "binary" {
				t.Fatalf("%s file input %q must be string/binary: %#v", def.Name, path, property)
			}
		}
		if len(def.FileArgRewritePaths) > 0 {
			paths, ok := meta["openai/fileParams"].([]string)
			if !ok || strings.Join(paths, ",") != strings.Join(def.FileArgRewritePaths, ",") {
				t.Fatalf("%s openai/fileParams = %#v, want %#v", def.Name, meta["openai/fileParams"], def.FileArgRewritePaths)
			}
		}

		outputProps, _ := def.OutputSchema["properties"].(map[string]any)
		for _, path := range def.FileResultRewritePaths {
			if _, ok := outputProps[path]; !ok {
				t.Fatalf("%s file output path %q missing from output schema", def.Name, path)
			}
		}
		if len(def.FileResultRewritePaths) > 0 {
			paths, ok := meta["openai/fileResultPaths"].([]string)
			if !ok || strings.Join(paths, ",") != strings.Join(def.FileResultRewritePaths, ",") {
				t.Fatalf("%s openai/fileResultPaths = %#v, want %#v", def.Name, meta["openai/fileResultPaths"], def.FileResultRewritePaths)
			}
		}
	}
}

func TestToolDescriptorsUseConfigAwareTaskManageSchema(t *testing.T) {
	withoutNexus := toolDescriptorsForConfig(t, []string{"task_manage"}, config.Config{})[0]
	withoutProps := withoutNexus["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if _, ok := withoutProps["template_id"]; ok {
		t.Fatal("task_manage descriptor should hide Nexus-only fields without Nexus")
	}
	withoutOutputProps := withoutNexus["outputSchema"].(map[string]any)["properties"].(map[string]any)
	if _, ok := withoutOutputProps["guidance_context"]; ok {
		t.Fatal("task_manage descriptor should hide Nexus-only output fields without Nexus")
	}

	withNexus := toolDescriptorsForConfig(t, []string{"task_manage"}, config.Config{NexusEndpoint: "http://127.0.0.1:18777"})[0]
	withProps := withNexus["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if _, ok := withProps["template_id"]; !ok {
		t.Fatal("task_manage descriptor should expose Nexus fields with Nexus")
	}
	withOutputProps := withNexus["outputSchema"].(map[string]any)["properties"].(map[string]any)
	if _, ok := withOutputProps["guidance_context"]; !ok {
		t.Fatal("task_manage descriptor should expose Nexus output fields with Nexus")
	}
}

func TestToolEnvelopeMCPImageStripsInternalBase64FromStructuredContent(t *testing.T) {
	response := toolEnvelope("view_image", map[string]any{
		"ok":                   true,
		"source":               map[string]any{"type": "artifact", "artifact_id": "artifact-1"},
		"_mcp_image_base64":    "YWJjMTIz",
		"_mcp_image_mime_type": "image/png",
	}, nil)
	content := response["content"].([]map[string]any)
	if content[0]["type"] != "image" || content[0]["data"] != "YWJjMTIz" || content[0]["mimeType"] != "image/png" {
		t.Fatalf("content = %#v", content)
	}
	structured := response["structuredContent"].(map[string]any)
	if _, ok := structured["_mcp_image_base64"]; ok {
		t.Fatalf("structuredContent leaked internal base64: %#v", structured)
	}
	if _, ok := structured["_mcp_image_mime_type"]; ok {
		t.Fatalf("structuredContent leaked internal mime type: %#v", structured)
	}
}

func TestToolCallResultFileEditUsesCompactTextContent(t *testing.T) {
	diff := strings.Repeat("+large diff line\n", 4096)
	response, err := toolCallResult("file_edit", map[string]any{
		"action": "add", "path": "main.go", "summary": "updated main.go", "diff_preview": diff,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	content, ok := response.Content[0].(*mcpsdk.TextContent)
	if !ok || content.Text != "updated main.go" {
		t.Fatalf("file_edit text content = %#v, want compact summary", response.Content)
	}
	structured := response.StructuredContent.(map[string]any)
	if structured["diff_preview"] != diff {
		t.Fatal("file_edit structuredContent lost diff_preview")
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(encoded), "+large diff line"); got != 4096 {
		t.Fatalf("file_edit response contains diff marker %d times, want 4096", got)
	}
}

func TestToolCallResultExecCommandDoesNotDuplicateOutputInTextContent(t *testing.T) {
	stdout := strings.Repeat("large-output-line\n", 4096)
	response, err := toolCallResult("exec_command", map[string]any{
		"exit_code": 0,
		"stdout":    stdout,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	content, ok := response.Content[0].(*mcpsdk.TextContent)
	if !ok || content.Text != "command exited: 0" {
		t.Fatalf("exec_command text content = %#v, want compact status", response.Content)
	}
	structured := response.StructuredContent.(map[string]any)
	if structured["stdout"] != stdout {
		t.Fatal("exec_command structuredContent lost stdout")
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(encoded), "large-output-line"); got != 4096 {
		t.Fatalf("exec_command response contains stdout marker %d times, want 4096", got)
	}
}

func fileEdit10792ByteRegressionPayload(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "transform_full_multikappa.py"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 10792 {
		t.Fatalf("regression payload size = %d, want 10792", len(data))
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != "7988ec11e020d6f34a826d25c3d2b1feece7cc9a1cb6cb1aabb2209671398e4b" {
		t.Fatalf("regression payload sha256 = %s", got)
	}
	return string(data)
}

func assertFileEditStatsPresent(t *testing.T, structured map[string]any) {
	t.Helper()
	for _, key := range []string{"files_changed", "insertions", "deletions"} {
		if _, exists := structured[key]; !exists {
			t.Fatalf("file_edit structuredContent missing %q: %#v", key, structured)
		}
	}
}

func TestToolEnvelopeFileEditUsesCompactTextContent(t *testing.T) {
	diff := strings.Repeat("+large diff line\n", 4096)
	response := toolEnvelope("file_edit", map[string]any{
		"action": "add", "path": "main.go", "summary": "updated main.go", "diff_preview": diff,
	}, nil)
	content := response["content"].([]map[string]any)
	if got := content[0]["text"]; got != "updated main.go" {
		t.Fatalf("file_edit text content = %#v, want compact summary", got)
	}
	structured := response["structuredContent"].(map[string]any)
	if structured["diff_preview"] != diff {
		t.Fatal("file_edit structuredContent lost diff_preview")
	}
}

func TestCallToolReportsMalformedArgumentDetails(t *testing.T) {
	raw := json.RawMessage(`{"path":`)
	_, err := (&Server{}).callTool(t.Context(), "file_edit", &mcpsdk.CallToolRequest{
		Params: &mcpsdk.CallToolParamsRaw{Arguments: raw},
	})
	if err == nil {
		t.Fatal("malformed tool arguments were accepted")
	}
	message := err.Error()
	for _, want := range []string{fmt.Sprintf("(%d bytes, offset %d)", len(raw), len(raw)), "unexpected end of JSON input"} {
		if !strings.Contains(message, want) {
			t.Fatalf("malformed argument error %q does not contain %q", message, want)
		}
	}
}

func TestToolEnvelopePassesThroughDynamicMCPContent(t *testing.T) {
	response := toolEnvelope("mcp_tool_call", map[string]any{
		"ok":   true,
		"name": "figma:get_screenshot",
		"result": map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": "done"},
				map[string]any{"type": "image", "data": "YWJjMTIz", "mimeType": "image/png"},
			},
			"structuredContent": map[string]any{"node_id": "1:2"},
		},
	}, nil)
	content, ok := response["content"].([]any)
	if !ok || len(content) != 2 {
		t.Fatalf("dynamic MCP content = %#v", response["content"])
	}
	image, _ := content[1].(map[string]any)
	if image["type"] != "image" || image["data"] != "YWJjMTIz" || image["mimeType"] != "image/png" {
		t.Fatalf("dynamic MCP image content = %#v", image)
	}
	structured, _ := response["structuredContent"].(map[string]any)
	if structured["name"] != "figma:get_screenshot" {
		t.Fatalf("structuredContent = %#v", structured)
	}
}

func TestOfficialSDKServerListsAndCallsAgentDockTools(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{AgentDockDefaultDir: root, AgentDockHome: filepath.Join(root, ".agentdock")}
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	defer runtime.Close()

	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	server := NewServer(runtime, cfg)
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.sdk.Run(t.Context(), serverTransport) }()

	client := mcpsdk.NewClient(
		&mcpsdk.Implementation{Name: "agentdock-test", Version: "1.0.0"},
		&mcpsdk.ClientOptions{Capabilities: &mcpsdk.ClientCapabilities{}},
	)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	foundAgentDockContext := false
	foundFilePublish := false
	for tool, err := range session.Tools(t.Context(), nil) {
		if err != nil {
			t.Fatalf("Tools() error = %v", err)
		}
		switch tool.Name {
		case "agentdock_context":
			foundAgentDockContext = true
		case "file_publish":
			foundFilePublish = true
		}
	}
	if !foundAgentDockContext || foundFilePublish {
		t.Fatalf("tool discovery mismatch: agentdock_context=%v file_publish=%v", foundAgentDockContext, foundFilePublish)
	}

	result, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "agentdock_context", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	structured, ok := result.StructuredContent.(map[string]any)
	runtimeInfo, runtimeOK := structured["runtime"].(map[string]any)
	if !ok || !runtimeOK || runtimeInfo["os"] == "" || runtimeInfo["path_model"] != config.PathModel || result.IsError {
		t.Fatalf("CallTool() result = %#v", result)
	}

	command := `printf compact-response`
	if gort.GOOS == "windows" {
		command = `[Console]::Write("compact-response")`
	}
	execResult, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "exec_command",
		Arguments: map[string]any{
			"cmd": command, "execution_mode": "sync",
		},
	})
	if err != nil || execResult.IsError {
		t.Fatalf("compact exec_command result=%#v err=%v", execResult, err)
	}
	execStructured, ok := execResult.StructuredContent.(map[string]any)
	if !ok || fmt.Sprint(execStructured["exit_code"]) != "0" || execStructured["stdout"] != "compact-response" {
		t.Fatalf("compact exec_command structuredContent = %#v", execResult.StructuredContent)
	}
	if len(execStructured) != 2 {
		t.Fatalf("successful exec_command exposed redundant fields: %#v", execStructured)
	}

	largeContent := fileEdit10792ByteRegressionPayload(t)
	fileEditResult, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "file_edit",
		Arguments: map[string]any{
			"action": "add", "path": "transform_full_multikappa.py", "content": largeContent,
		},
	})
	if err != nil || fileEditResult.IsError {
		t.Fatalf("large file_edit result=%#v err=%v", fileEditResult, err)
	}
	fileEditStructured, ok := fileEditResult.StructuredContent.(map[string]any)
	if !ok || fileEditStructured["changed"] != true {
		t.Fatalf("large file_edit structuredContent = %#v", fileEditResult.StructuredContent)
	}
	assertFileEditStatsPresent(t, fileEditStructured)
	if _, exists := fileEditStructured["diff_preview"]; exists {
		t.Fatalf("large applied file_edit returned an unsolicited diff: %#v", fileEditStructured)
	}
	encodedResult, err := json.Marshal(fileEditResult)
	if err != nil {
		t.Fatal(err)
	}
	if len(encodedResult) > 4096 {
		t.Fatalf("large file_edit response echoed input: %d bytes", len(encodedResult))
	}
	written, err := os.ReadFile(filepath.Join(root, "transform_full_multikappa.py"))
	if err != nil || string(written) != largeContent {
		t.Fatalf("large file_edit content mismatch: bytes=%d err=%v", len(written), err)
	}

	retired, retiredErr := session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "file_publish", Arguments: map[string]any{"path": "README.md"}})
	if retiredErr == nil && retired != nil && !retired.IsError {
		t.Fatalf("retired file_publish unexpectedly succeeded: %#v", retired)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("Server.Run() error = %v", err)
	}
}

func TestStreamableHTTPHandlerAllowsAuthenticatedPublicHost(t *testing.T) {
	for name, cfg := range map[string]config.Config{
		"static token": {
			AuthToken:      "configured-token",
			OAuthServerURL: "https://dockmini.example",
		},
		"OAuth": {
			OAuthEnabled:   true,
			OAuthServerURL: "https://dockmini.example",
		},
	} {
		t.Run(name, func(t *testing.T) {
			response := serveStreamableHTTPRequest(t, cfg)
			if response.Code == http.StatusForbidden {
				t.Fatalf("authenticated public Host was rejected: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestStreamableHTTPHandlerKeepsLocalhostProtection(t *testing.T) {
	for name, cfg := range map[string]config.Config{
		"no public URL": {
			AuthToken: "configured-token",
		},
		"public URL without authentication": {
			OAuthServerURL: "https://dockmini.example",
		},
	} {
		t.Run(name, func(t *testing.T) {
			response := serveStreamableHTTPRequest(t, cfg)
			if response.Code != http.StatusForbidden {
				t.Fatalf("untrusted public Host status=%d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), "invalid Host header") {
				t.Fatalf("unexpected rejection body: %s", response.Body.String())
			}
		})
	}
}

func serveStreamableHTTPRequest(t *testing.T, cfg config.Config) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://dockmini.example/mcp", nil)
	request.Host = "dockmini.example"
	localAddress := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 18766}
	request = request.WithContext(context.WithValue(request.Context(), http.LocalAddrContextKey, localAddress))
	response := httptest.NewRecorder()
	NewServer(nil, cfg).HTTPHandler().ServeHTTP(response, request)
	return response
}

func toolDescriptorsForConfig(t *testing.T, names []string, cfg config.Config) []map[string]any {
	t.Helper()
	root := t.TempDir()
	cfg.AgentDockDefaultDir = root
	cfg.AgentDockHome = filepath.Join(root, ".agentdock")
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("normalize runtime config: %v", err)
	}
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("close runtime: %v", err)
		}
	})
	definitions := make([]ToolDefinition, 0, len(names))
	for _, name := range names {
		definition, ok := runtime.ToolDefinition(name)
		if !ok {
			t.Fatalf("tool %s is not available for test config", name)
		}
		definitions = append(definitions, definition)
	}
	return toolDescriptors(definitions, true)
}
