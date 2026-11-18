// Package mcp is the forge's Model Context Protocol endpoint at POST /mcp
// (see handler.go).
package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forge/internal/service"
	"forge/internal/store/embedded"
)

func testAuth(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") != "Bearer test-token" {
		writeError(w, http.StatusUnauthorized, "Bad credentials")
		return false
	}
	return true
}

func testSetup(t *testing.T) (*service.Service, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	st, err := embedded.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := service.New(st, dir, "adaiversen")
	return svc, New(svc, testAuth)
}

// rpc sends one JSON-RPC request and returns the HTTP status and the
// decoded response object.
func rpc(t *testing.T, h http.Handler, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var resp map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("MCP response is not JSON (HTTP %d): %s", rec.Code, rec.Body.String())
		}
	}
	return rec.Code, resp
}

// callTool calls a tool and returns its result object (the tools/call
// result), failing the test on a JSON-RPC error or a tool error.
func callTool(t *testing.T, h http.Handler, id any, name string, args map[string]any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	})
	_, resp := rpc(t, h, string(body))
	if resp["error"] != nil {
		t.Fatalf("tools/call %s: JSON-RPC error %v", name, resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/call %s: no result in %v", name, resp)
	}
	if result["isError"] == true {
		t.Fatalf("tools/call %s: tool error: %v", name, result["content"])
	}
	return result
}

func TestDiscoverAndInitialize(t *testing.T) {
	_, h := testSetup(t)
	_, resp := rpc(t, h, `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{}}`)
	result := resp["result"].(map[string]any)
	versions, _ := result["supportedVersions"].([]any)
	found := false
	for _, v := range versions {
		if v == "2026-07-28" {
			found = true
		}
	}
	if !found {
		t.Fatalf("server/discover does not list 2026-07-28: %v", versions)
	}
	meta := result["_meta"].(map[string]any)
	if meta["io.modelcontextprotocol/serverInfo"].(map[string]any)["name"] != "forge" {
		t.Fatalf("server/discover _meta has no serverInfo: %v", result)
	}
	if result["serverInfo"].(map[string]any)["name"] != "forge" {
		t.Fatal("server/discover has no loose serverInfo")
	}

	_, resp = rpc(t, h, `{"jsonrpc":"2.0","id":"init-1","method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	result = resp["result"].(map[string]any)
	if result["protocolVersion"] != "2025-11-25" {
		t.Fatalf("initialize negotiated %v, want 2025-11-25", result["protocolVersion"])
	}
	if result["serverInfo"].(map[string]any)["name"] != "forge" {
		t.Fatal("initialize has no serverInfo")
	}

	// ping answers an empty result.
	_, resp = rpc(t, h, `{"jsonrpc":"2.0","id":2,"method":"ping","params":{}}`)
	if _, ok := resp["result"].(map[string]any); !ok {
		t.Fatalf("ping has no result: %v", resp)
	}
}

func TestNotificationAndParseError(t *testing.T) {
	_, h := testSetup(t)
	req := httptest.NewRequest("POST", "/mcp",
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`)))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 202 || rec.Body.Len() != 0 {
		t.Fatalf("a notification must answer 202 with an empty body, got HTTP %d: %q", rec.Code, rec.Body.String())
	}

	// A truncated body is HTTP 400 with a parse error that still carries
	// an id.
	status, resp := rpc(t, h, `{"jsonrpc": "2.0", "id": 424242, "method": "tools/list"`)
	if status != 400 {
		t.Fatalf("a non-JSON body: HTTP %d, want 400", status)
	}
	errObj := resp["error"].(map[string]any)
	if errObj["code"] != float64(-32700) {
		t.Fatalf("the parse error code is %v, want -32700", errObj["code"])
	}
	if _, ok := resp["id"]; !ok {
		t.Fatal("the parse error response carries no id")
	}
}

func TestToolsList(t *testing.T) {
	_, h := testSetup(t)
	// The same request id twice: the two answers must be byte-identical.
	body := `{"jsonrpc":"2.0","id":"list-1","method":"tools/list","params":{}}`
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-token")
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req)
	req = httptest.NewRequest("POST", "/mcp", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-token")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec1.Body.String() != rec2.Body.String() {
		t.Fatal("tools/list is not byte-identical across calls")
	}
	var resp map[string]any
	if err := json.Unmarshal(rec1.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	tools := resp["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, tool := range tools {
		m := tool.(map[string]any)
		names[m["name"].(string)] = true
		if m["description"] == "" {
			t.Fatalf("tool %q has no description", m["name"])
		}
		schema, ok := m["inputSchema"].(map[string]any)
		if !ok || schema["type"] != "object" {
			t.Fatalf("tool %q has no object inputSchema", m["name"])
		}
	}
	for _, want := range []string{"create_repo", "get_repo", "create_issue", "list_issues", "create_pull_request", "merge_pull_request"} {
		if !names[want] {
			t.Fatalf("tools/list misses %q", want)
		}
	}
	if len(tools) < 6 {
		t.Fatalf("tools/list offers %d tools, want at least 6", len(tools))
	}
}

func TestToolsEndToEnd(t *testing.T) {
	svc, h := testSetup(t)

	result := callTool(t, h, 1, "create_repo", map[string]any{"name": "m1", "description": "d", "private": true})
	created := result["structuredContent"].(map[string]any)
	if created["name"] != "m1" || created["private"] != true {
		t.Fatalf("create_repo result is wrong: %v", created)
	}
	if _, err := svc.GetRepository("adaiversen", "m1"); err != nil {
		t.Fatalf("create_repo did not create the repository: %v", err)
	}

	result = callTool(t, h, 2, "get_repo", map[string]any{"owner": "adaiversen", "repo": "m1"})
	if result["structuredContent"].(map[string]any)["name"] != "m1" {
		t.Fatalf("get_repo result is wrong: %v", result)
	}

	result = callTool(t, h, 3, "create_issue", map[string]any{"owner": "adaiversen", "repo": "m1", "title": "mcp issue", "body": "b"})
	issue := result["structuredContent"].(map[string]any)
	if issue["title"] != "mcp issue" || issue["number"] == nil {
		t.Fatalf("create_issue result is wrong: %v", issue)
	}
	issues, err := svc.ListIssues("adaiversen", "m1", service.IssueListOptions{State: "all"})
	if err != nil || len(issues) != 1 || issues[0].Title != "mcp issue" {
		t.Fatalf("the issue created over MCP is not visible over the service: %v, %v", issues, err)
	}

	result = callTool(t, h, 4, "list_issues", map[string]any{"owner": "adaiversen", "repo": "m1", "state": "open"})
	text := ""
	for _, block := range result["content"].([]any) {
		text += block.(map[string]any)["text"].(string)
	}
	if !strings.Contains(text, "mcp issue") {
		t.Fatalf("list_issues does not return the issue: %q", text)
	}

	// A pull request: create the branches through the service, then open
	// and merge it over MCP.
	if _, err := svc.PutContent("adaiversen", "m1", "a.txt", []byte("one\n"), "base", "", ""); err != nil {
		t.Fatal(err)
	}
	branches, err := svc.ListBranches("adaiversen", "m1")
	if err != nil || len(branches) == 0 {
		t.Fatal(err)
	}
	if _, err := svc.CreateRef("adaiversen", "m1", "refs/heads/feature", branches[0].SHA); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PutContent("adaiversen", "m1", "b.txt", []byte("two\n"), "feature work", "feature", ""); err != nil {
		t.Fatal(err)
	}
	result = callTool(t, h, 5, "create_pull_request", map[string]any{
		"owner": "adaiversen", "repo": "m1", "title": "add b", "head": "feature", "base": "main"})
	pr := result["structuredContent"].(map[string]any)
	if pr["number"] == nil || pr["head"].(map[string]any)["ref"] != "feature" {
		t.Fatalf("create_pull_request result is wrong: %v", pr)
	}
	result = callTool(t, h, 6, "merge_pull_request", map[string]any{
		"owner": "adaiversen", "repo": "m1", "pull_number": int(pr["number"].(float64))})
	if result["structuredContent"].(map[string]any)["merged"] != true {
		t.Fatalf("merge_pull_request result is wrong: %v", result)
	}
	got, err := svc.GetPull("adaiversen", "m1", 1)
	if err != nil || !got.Merged {
		t.Fatalf("the pull request is not merged over the service: %v, %v", got, err)
	}
}

func TestUnknownToolAndMissingArgs(t *testing.T) {
	_, h := testSetup(t)
	_, resp := rpc(t, h, `{"jsonrpc":"2.0","id":"u1","method":"tools/call","params":{"name":"no_such_tool","arguments":{}}}`)
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("an unknown tool must answer a JSON-RPC error, got: %v", resp)
	}
	if errObj["code"] != float64(-32601) && errObj["code"] != float64(-32602) {
		t.Fatalf("the unknown-tool error code is %v, want -32601 or -32602", errObj["code"])
	}

	// create_issue with no arguments is a tool error (isError:true).
	result := callToolRaw(t, h, "x1", "create_issue", map[string]any{})
	if result["isError"] != true {
		t.Fatalf("create_issue with no arguments was accepted: %v", result)
	}
}

// callToolRaw calls a tool and returns its result object without failing
// on a tool error.
func callToolRaw(t *testing.T, h http.Handler, id any, name string, args map[string]any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	})
	_, resp := rpc(t, h, string(body))
	if resp["error"] != nil {
		t.Fatalf("tools/call %s: JSON-RPC error %v", name, resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/call %s: no result in %v", name, resp)
	}
	return result
}

func TestConcurrentCallsEchoTheirIds(t *testing.T) {
	_, h := testSetup(t)
	// Two calls with distinct string ids, issued one after the other
	// (the handler is synchronous, so the echo is what matters).
	_, resp1 := rpc(t, h, `{"jsonrpc":"2.0","id":"left","method":"tools/list","params":{}}`)
	_, resp2 := rpc(t, h, `{"jsonrpc":"2.0","id":"right","method":"tools/list","params":{}}`)
	if resp1["id"] != "left" || resp2["id"] != "right" {
		t.Fatalf("the responses did not echo their ids: %v / %v", resp1["id"], resp2["id"])
	}
}

func TestNoAuth(t *testing.T) {
	_, h := testSetup(t)
	req := httptest.NewRequest("POST", "/mcp",
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("no token: HTTP %d, want 401", rec.Code)
	}
}
