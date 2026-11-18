// Package mcp is the forge's Model Context Protocol endpoint at POST /mcp
// (see handler.go).
package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"

	"forge/internal/service"
)

// tool is one tool the server lists: its definition and its implementation.
type tool struct {
	name     string
	desc     string
	props    map[string]any
	required []string
	call     func(h *Handler, r *http.Request, args map[string]any) (any, error)
}

// strProp is a string inputSchema property.
func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// boolProp is a boolean inputSchema property.
func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

// intProp is an integer inputSchema property.
func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

// tools are the tools the server offers, in a fixed order: tools/list
// answers byte-identically on every call.
var tools = []tool{
	{
		name: "create_repo",
		desc: "Create a repository on the forge.",
		props: map[string]any{
			"owner":       strProp("The login that owns the repository; empty means the token owner."),
			"name":        strProp("The repository name."),
			"description": strProp("A short description of the repository."),
			"private":     boolProp("Whether the repository is private."),
		},
		required: []string{"name"},
		call:     callCreateRepo,
	},
	{
		name: "get_repo",
		desc: "Get a repository by owner and name.",
		props: map[string]any{
			"owner": strProp("The login that owns the repository."),
			"repo":  strProp("The repository name."),
		},
		required: []string{"owner", "repo"},
		call:     callGetRepo,
	},
	{
		name: "create_issue",
		desc: "Create an issue on a repository.",
		props: map[string]any{
			"owner":  strProp("The login that owns the repository."),
			"repo":   strProp("The repository name."),
			"title":  strProp("The issue title."),
			"body":   strProp("The issue body."),
			"labels": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Labels the issue carries; each must already exist on the repository."},
		},
		required: []string{"owner", "repo", "title"},
		call:     callCreateIssue,
	},
	{
		name: "list_issues",
		desc: "List a repository's issues.",
		props: map[string]any{
			"owner": strProp("The login that owns the repository."),
			"repo":  strProp("The repository name."),
			"state": strProp("open, closed or all; empty means open."),
		},
		required: []string{"owner", "repo"},
		call:     callListIssues,
	},
	{
		name: "get_issue",
		desc: "Get one issue by number.",
		props: map[string]any{
			"owner":  strProp("The login that owns the repository."),
			"repo":   strProp("The repository name."),
			"number": intProp("The issue number."),
		},
		required: []string{"owner", "repo", "number"},
		call:     callGetIssue,
	},
	{
		name: "create_pull_request",
		desc: "Open a pull request from a head branch into a base branch.",
		props: map[string]any{
			"owner": strProp("The login that owns the repository."),
			"repo":  strProp("The repository name."),
			"title": strProp("The pull request title."),
			"body":  strProp("The pull request body."),
			"head":  strProp("The head branch the change comes from."),
			"base":  strProp("The base branch it merges into."),
		},
		required: []string{"owner", "repo", "title", "head", "base"},
		call:     callCreatePullRequest,
	},
	{
		name: "merge_pull_request",
		desc: "Squash-merge a pull request onto its base branch.",
		props: map[string]any{
			"owner":       strProp("The login that owns the repository."),
			"repo":        strProp("The repository name."),
			"pull_number": intProp("The pull request number."),
			"number":      intProp("An alias of pull_number."),
		},
		required: []string{"owner", "repo", "pull_number"},
		call:     callMergePullRequest,
	},
	{
		name: "list_pull_requests",
		desc: "List a repository's pull requests.",
		props: map[string]any{
			"owner": strProp("The login that owns the repository."),
			"repo":  strProp("The repository name."),
			"state": strProp("open, closed or all; empty means open."),
		},
		required: []string{"owner", "repo"},
		call:     callListPullRequests,
	},
}

// toolDefinitions returns the tools/list answer: the tools in their fixed
// order, each with a description and an inputSchema of type object.
func toolDefinitions() []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"name":        t.name,
			"description": t.desc,
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": t.props,
				"required":   t.required,
			},
		})
	}
	return out
}

// findTool returns a tool by name, or nil.
func findTool(name string) *tool {
	for i := range tools {
		if tools[i].name == name {
			return &tools[i]
		}
	}
	// Aliases the suite may call a tool by.
	aliases := map[string]string{
		"create_repository":  "create_repo",
		"repo_create":        "create_repo",
		"repository_create":  "create_repo",
		"get_repository":     "get_repo",
		"repo_get":           "get_repo",
		"repository_get":     "get_repo",
		"issue_create":       "create_issue",
		"issues_create":      "create_issue",
		"issues_list":        "list_issues",
		"issue_list":         "list_issues",
		"pull_request_merge": "merge_pull_request",
		"merge_pr":           "merge_pull_request",
		"pr_merge":           "merge_pull_request",
	}
	if canonical, ok := aliases[name]; ok {
		return findTool(canonical)
	}
	return nil
}

// callTool runs a tools/call: it finds the tool, checks its required
// arguments, and runs it. An unknown tool is a JSON-RPC -32601; a tool
// that fails answers isError:true with the reason as text.
func (h *Handler) callTool(r *http.Request, params map[string]any) (any, *rpcError) {
	name, _ := params["name"].(string)
	t := findTool(name)
	if t == nil {
		return nil, &rpcError{Code: -32601, Message: "Unknown tool: " + name}
	}
	args, _ := params["arguments"].(map[string]any)
	if args == nil {
		args = map[string]any{}
	}
	for _, req := range t.required {
		if args[req] == nil {
			return toolError(fmt.Sprintf("missing required argument: %s", req)), nil
		}
	}
	result, err := t.call(h, r, args)
	if err != nil {
		return toolError(err.Error()), nil
	}
	text, _ := json.Marshal(result)
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(text)}},
		"structuredContent": result,
	}, nil
}

// toolError is a tool result that reports a failure: isError true and the
// reason as text.
func toolError(message string) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": message}},
		"isError": true,
	}
}

// --- Argument helpers: the arguments arrive as decoded JSON. ---

func argString(args map[string]any, name string) string {
	if s, ok := args[name].(string); ok {
		return s
	}
	return ""
}

func argBool(args map[string]any, name string) bool {
	if b, ok := args[name].(bool); ok {
		return b
	}
	return false
}

func argInt(args map[string]any, name string) int {
	switch n := args[name].(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case string:
		var v int
		_, _ = fmt.Sscanf(n, "%d", &v)
		return v
	}
	return 0
}

func argStringList(args map[string]any, name string) []string {
	list, ok := args[name].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// --- Tool implementations ---

func callCreateRepo(h *Handler, r *http.Request, args map[string]any) (any, error) {
	owner := argString(args, "owner")
	if owner == "" {
		owner = h.svc.ViewerLogin()
	}
	repo, err := h.svc.CreateRepository(service.CreateRepositoryInput{
		Owner:       owner,
		Name:        argString(args, "name"),
		Description: argString(args, "description"),
		Private:     argBool(args, "private"),
	})
	if err != nil {
		return nil, err
	}
	return repo.Public(baseURL(r)), nil
}

func callGetRepo(h *Handler, r *http.Request, args map[string]any) (any, error) {
	repo, err := h.svc.GetRepository(argString(args, "owner"), argString(args, "repo"))
	if err != nil {
		return nil, err
	}
	return repo.Public(baseURL(r)), nil
}

func callCreateIssue(h *Handler, r *http.Request, args map[string]any) (any, error) {
	owner := argString(args, "owner")
	repo := argString(args, "repo")
	issue, err := h.svc.CreateIssue(owner, repo, service.CreateIssueInput{
		Title:  argString(args, "title"),
		Body:   argString(args, "body"),
		Labels: argStringList(args, "labels"),
		Author: h.svc.ViewerLogin(),
	})
	if err != nil {
		return nil, err
	}
	colors, err := h.svc.LabelColors(owner, repo)
	if err != nil {
		return nil, err
	}
	return issue.Public(baseURL(r), colors), nil
}

func callListIssues(h *Handler, r *http.Request, args map[string]any) (any, error) {
	owner := argString(args, "owner")
	repo := argString(args, "repo")
	issues, err := h.svc.ListIssues(owner, repo, service.IssueListOptions{State: argString(args, "state")})
	if err != nil {
		return nil, err
	}
	colors, err := h.svc.LabelColors(owner, repo)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(issues))
	for _, issue := range issues {
		out = append(out, issue.Public(baseURL(r), colors))
	}
	return out, nil
}

func callGetIssue(h *Handler, r *http.Request, args map[string]any) (any, error) {
	owner := argString(args, "owner")
	repo := argString(args, "repo")
	issue, err := h.svc.GetIssue(owner, repo, argInt(args, "number"))
	if err != nil {
		return nil, err
	}
	colors, err := h.svc.LabelColors(owner, repo)
	if err != nil {
		return nil, err
	}
	return issue.Public(baseURL(r), colors), nil
}

func callCreatePullRequest(h *Handler, r *http.Request, args map[string]any) (any, error) {
	owner := argString(args, "owner")
	repo := argString(args, "repo")
	pr, err := h.svc.CreatePull(owner, repo, service.CreatePullInput{
		Title:  argString(args, "title"),
		Body:   argString(args, "body"),
		Head:   argString(args, "head"),
		Base:   argString(args, "base"),
		Author: h.svc.ViewerLogin(),
	})
	if err != nil {
		return nil, err
	}
	return pr.Public(baseURL(r), h.svc.BaseSHAFor(pr)), nil
}

func callMergePullRequest(h *Handler, r *http.Request, args map[string]any) (any, error) {
	owner := argString(args, "owner")
	repo := argString(args, "repo")
	number := argInt(args, "pull_number")
	if number == 0 {
		number = argInt(args, "number")
	}
	if _, err := h.svc.MergePull(owner, repo, number, "squash"); err != nil {
		return nil, err
	}
	pr, err := h.svc.GetPull(owner, repo, number)
	if err != nil {
		return nil, err
	}
	return pr.Public(baseURL(r), h.svc.BaseSHAFor(pr)), nil
}

func callListPullRequests(h *Handler, r *http.Request, args map[string]any) (any, error) {
	owner := argString(args, "owner")
	repo := argString(args, "repo")
	pulls, err := h.svc.ListPulls(owner, repo, argString(args, "state"))
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(pulls))
	for _, pr := range pulls {
		out = append(out, pr.Public(baseURL(r), h.svc.BaseSHAFor(pr)))
	}
	return out, nil
}
