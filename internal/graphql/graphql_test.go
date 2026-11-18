// Package graphql is the forge's GraphQL API at POST /api/graphql (see
// handler.go).
package graphql

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

// gql runs one GraphQL document against the handler and returns the
// decoded response object.
func gql(t *testing.T, h http.Handler, query string, variables map[string]any) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/graphql", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GraphQL HTTP %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("GraphQL response is not JSON: %s", rec.Body.String())
	}
	return resp
}

// data returns the response's data object, failing the test when the
// response carries errors.
func data(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	if errs, ok := resp["errors"]; ok {
		t.Fatalf("GraphQL errors: %v", errs)
	}
	d, ok := resp["data"].(map[string]any)
	if !ok {
		t.Fatalf("GraphQL response has no data object: %v", resp)
	}
	return d
}

func fieldNames(t *testing.T, fields []any) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, f := range fields {
		m, ok := f.(map[string]any)
		if !ok {
			t.Fatalf("introspection field is not an object: %v", f)
		}
		out[m["name"].(string)] = true
	}
	return out
}

func TestViewerLogin(t *testing.T) {
	_, h := testSetup(t)
	resp := gql(t, h, `query { viewer { login } }`, nil)
	if got := data(t, resp)["viewer"].(map[string]any)["login"]; got != "adaiversen" {
		t.Fatalf("viewer.login = %v, want adaiversen", got)
	}
}

func TestRepositoryFieldsAndIntrospection(t *testing.T) {
	svc, h := testSetup(t)
	repo, err := svc.CreateRepository(service.CreateRepositoryInput{Name: "g1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PutContent("adaiversen", "g1", "README.md", []byte("hi\n"), "first", "", ""); err != nil {
		t.Fatal(err)
	}

	resp := gql(t, h, `query($owner: String!, $name: String!) {
		repository(owner: $owner, name: $name) {
			id databaseId name nameWithOwner viewerPermission hasIssuesEnabled
			owner { login }
			defaultBranchRef { name }
		}
	}`, map[string]any{"owner": "adaiversen", "name": "g1"})
	r := data(t, resp)["repository"].(map[string]any)
	if r["databaseId"] != float64(repo.ID) || r["name"] != "g1" ||
		r["nameWithOwner"] != "adaiversen/g1" || r["viewerPermission"] != "ADMIN" ||
		r["hasIssuesEnabled"] != true {
		t.Fatalf("repository fields are wrong: %v", r)
	}
	if r["owner"].(map[string]any)["login"] != "adaiversen" {
		t.Fatalf("repository.owner is wrong: %v", r["owner"])
	}
	if r["defaultBranchRef"].(map[string]any)["name"] != "main" {
		t.Fatalf("defaultBranchRef is wrong: %v", r["defaultBranchRef"])
	}
	if r["id"] == "" {
		t.Fatal("repository.id is empty")
	}

	// An unknown repository is null, not an error.
	resp = gql(t, h, `query { repository(owner: "adaiversen", name: "nope") { name } }`, nil)
	if data(t, resp)["repository"] != nil {
		t.Fatal("an unknown repository must answer null")
	}

	// Introspection: Issue, PullRequest, Repository and SearchType.
	resp = gql(t, h, `query { __type(name: "Issue") { fields(includeDeprecated: true) { name } } }`, nil)
	fields := fieldNames(t, data(t, resp)["__type"].(map[string]any)["fields"].([]any))
	for _, want := range []string{"id", "number", "title", "state", "body"} {
		if !fields[want] {
			t.Fatalf("Issue introspection misses %q", want)
		}
	}
	resp = gql(t, h, `query { __type(name: "PullRequest") { fields { name } } }`, nil)
	fields = fieldNames(t, data(t, resp)["__type"].(map[string]any)["fields"].([]any))
	for _, want := range []string{"id", "number", "headRefName", "baseRefName", "mergeable", "state", "merged", "files"} {
		if !fields[want] {
			t.Fatalf("PullRequest introspection misses %q", want)
		}
	}
	resp = gql(t, h, `query { __type(name: "Repository") { fields { name } } }`, nil)
	fields = fieldNames(t, data(t, resp)["__type"].(map[string]any)["fields"].([]any))
	for _, want := range []string{"id", "databaseId", "name", "nameWithOwner", "owner", "defaultBranchRef", "viewerPermission", "hasIssuesEnabled", "issue", "issues", "pullRequests", "pullRequest"} {
		if !fields[want] {
			t.Fatalf("Repository introspection misses %q", want)
		}
	}
	resp = gql(t, h, `query { __type(name: "SearchType") { enumValues(includeDeprecated: true) { name } } }`, nil)
	fields = fieldNames(t, data(t, resp)["__type"].(map[string]any)["enumValues"].([]any))
	if !fields["ISSUE"] {
		t.Fatal("SearchType introspection misses ISSUE")
	}
}

func TestIssuesOpenOrderAndPagination(t *testing.T) {
	svc, h := testSetup(t)
	if _, err := svc.CreateRepository(service.CreateRepositoryInput{Name: "g2"}); err != nil {
		t.Fatal(err)
	}
	mk := func(title string) {
		if _, err := svc.CreateIssue("adaiversen", "g2", service.CreateIssueInput{Title: title, Author: "adaiversen"}); err != nil {
			t.Fatal(err)
		}
	}
	mk("one")
	mk("two")
	mk("three")
	closed := "closed"
	if _, err := svc.UpdateIssue("adaiversen", "g2", 2, service.UpdateIssueInput{State: &closed}); err != nil {
		t.Fatal(err)
	}

	resp := gql(t, h, `query {
		repository(owner: "adaiversen", name: "g2") {
			issues(first: 100, states: [OPEN], orderBy: {field: CREATED_AT, direction: DESC}) {
				nodes { number title state id }
			}
		}
	}`, nil)
	nodes := data(t, resp)["repository"].(map[string]any)["issues"].(map[string]any)["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("open issues = %d, want 2", len(nodes))
	}
	n1 := int(nodes[0].(map[string]any)["number"].(float64))
	n2 := int(nodes[1].(map[string]any)["number"].(float64))
	if n1 < n2 {
		t.Fatalf("open issues are not in DESC order: %d then %d", n1, n2)
	}

	// Pagination: 7 issues in a second repo, ascending, 5 + 2.
	if _, err := svc.CreateRepository(service.CreateRepositoryInput{Name: "g3"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		if _, err := svc.CreateIssue("adaiversen", "g3", service.CreateIssueInput{Title: "p", Author: "adaiversen"}); err != nil {
			t.Fatal(err)
		}
	}
	query := `query($after: String) {
		repository(owner: "adaiversen", name: "g3") {
			issues(first: 5, after: $after, orderBy: {field: CREATED_AT, direction: ASC}) {
				nodes { number }
				pageInfo { hasNextPage endCursor }
			}
		}
	}`
	resp = gql(t, h, query, map[string]any{"after": nil})
	conn := data(t, resp)["repository"].(map[string]any)["issues"].(map[string]any)
	page1 := conn["nodes"].([]any)
	if len(page1) != 5 || conn["pageInfo"].(map[string]any)["hasNextPage"] != true {
		t.Fatalf("page 1 is wrong: %v", conn)
	}
	end := conn["pageInfo"].(map[string]any)["endCursor"].(string)
	resp = gql(t, h, query, map[string]any{"after": end})
	conn = data(t, resp)["repository"].(map[string]any)["issues"].(map[string]any)
	page2 := conn["nodes"].([]any)
	if len(page2) != 2 || conn["pageInfo"].(map[string]any)["hasNextPage"] != false {
		t.Fatalf("page 2 is wrong: %v", conn)
	}
	seen := map[float64]bool{}
	for _, n := range append(page1, page2...) {
		num := n.(map[string]any)["number"].(float64)
		if seen[num] {
			t.Fatalf("issue #%v appears on both pages", num)
		}
		seen[num] = true
	}
}

func TestMutations(t *testing.T) {
	svc, h := testSetup(t)
	repo, err := svc.CreateRepository(service.CreateRepositoryInput{Name: "g4"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PutContent("adaiversen", "g4", "a.txt", []byte("one\n"), "base", "", ""); err != nil {
		t.Fatal(err)
	}
	sha, err := svc.CreateRef("adaiversen", "g4", "refs/heads/feature", mustCommitSHA(t, svc))
	if err != nil {
		t.Fatal(err)
	}
	_ = sha
	if _, err := svc.PutContent("adaiversen", "g4", "b.txt", []byte("two\n"), "feature work", "feature", ""); err != nil {
		t.Fatal(err)
	}
	repoID := service.GlobalID("Repository", repo.ID)

	resp := gql(t, h, `mutation($input: CreateIssueInput!) {
		createIssue(input: $input) { issue { number id title state } }
	}`, map[string]any{"input": map[string]any{"repositoryId": repoID, "title": "gql issue", "body": "b"}})
	issue := data(t, resp)["createIssue"].(map[string]any)["issue"].(map[string]any)
	if issue["number"] != float64(1) || issue["state"] != "OPEN" {
		t.Fatalf("createIssue is wrong: %v", issue)
	}
	issueID := issue["id"].(string)

	resp = gql(t, h, `mutation($input: CloseIssueInput!) {
		closeIssue(input: $input) { issue { number state } }
	}`, map[string]any{"input": map[string]any{"issueId": issueID}})
	if data(t, resp)["closeIssue"].(map[string]any)["issue"].(map[string]any)["state"] != "CLOSED" {
		t.Fatal("closeIssue did not close the issue")
	}

	resp = gql(t, h, `mutation($input: AddCommentInput!) {
		addComment(input: $input) { commentEdge { node { id body } } }
	}`, map[string]any{"input": map[string]any{"subjectId": issueID, "body": "a comment"}})
	comment := data(t, resp)["addComment"].(map[string]any)["commentEdge"].(map[string]any)["node"].(map[string]any)
	if comment["body"] != "a comment" || comment["id"] == "" {
		t.Fatalf("addComment is wrong: %v", comment)
	}
	comments, err := svc.ListComments("adaiversen", "g4", 1)
	if err != nil || len(comments) != 1 {
		t.Fatalf("the comment is not visible over the service: %v, %v", comments, err)
	}

	resp = gql(t, h, `mutation($input: CreatePullRequestInput!) {
		createPullRequest(input: $input) { pullRequest { number state headRefName baseRefName mergeable } }
	}`, map[string]any{"input": map[string]any{"repositoryId": repoID, "baseRefName": "main", "headRefName": "feature", "title": "add b"}})
	pr := data(t, resp)["createPullRequest"].(map[string]any)["pullRequest"].(map[string]any)
	if pr["number"] != float64(1) || pr["state"] != "OPEN" || pr["headRefName"] != "feature" ||
		pr["baseRefName"] != "main" || pr["mergeable"] != "MERGEABLE" {
		t.Fatalf("createPullRequest is wrong: %v", pr)
	}

	// The pull request's files.
	resp = gql(t, h, `query {
		repository(owner: "adaiversen", name: "g4") {
			pullRequest(number: 1) { files(first: 10) { nodes { path additions deletions changes } } }
		}
	}`, nil)
	files := data(t, resp)["repository"].(map[string]any)["pullRequest"].(map[string]any)["files"].(map[string]any)["nodes"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["path"] != "b.txt" {
		t.Fatalf("pullRequest files are wrong: %v", files)
	}

	prID := ""
	// Read the pull request's global id back through the repository first.
	resp = gql(t, h, `query {
		repository(owner: "adaiversen", name: "g4") { pullRequest(number: 1) { id } }
	}`, nil)
	prID = data(t, resp)["repository"].(map[string]any)["pullRequest"].(map[string]any)["id"].(string)
	resp = gql(t, h, `mutation($input: MergePullRequestInput!) {
		mergePullRequest(input: $input) { pullRequest { number merged state } }
	}`, map[string]any{"input": map[string]any{"pullRequestId": prID, "mergeMethod": "SQUASH"}})
	merged := data(t, resp)["mergePullRequest"].(map[string]any)["pullRequest"].(map[string]any)
	if merged["merged"] != true || merged["state"] != "MERGED" {
		t.Fatalf("mergePullRequest is wrong: %v", merged)
	}

	// node(id:) round-trips a repository.
	resp = gql(t, h, `query($id: ID!) {
		node(id: $id) { id ... on Repository { name nameWithOwner } }
	}`, map[string]any{"id": repoID})
	node := data(t, resp)["node"].(map[string]any)
	if node["name"] != "g4" || node["nameWithOwner"] != "adaiversen/g4" {
		t.Fatalf("node(id:) is wrong: %v", node)
	}
}

// mustCommitSHA returns the tip of the default branch, for the branch
// fixture in TestMutations.
func mustCommitSHA(t *testing.T, svc *service.Service) string {
	t.Helper()
	branches, err := svc.ListBranches("adaiversen", "g4")
	if err != nil || len(branches) == 0 {
		t.Fatalf("no branches: %v", err)
	}
	return branches[0].SHA
}

func TestInvalidField(t *testing.T) {
	_, h := testSetup(t)
	resp := gql(t, h, `query { viewer { noSuchFieldOnViewer } }`, nil)
	errs, ok := resp["errors"].([]any)
	if !ok || len(errs) == 0 {
		t.Fatalf("an invalid field must produce errors, got: %v", resp)
	}
	msg := errs[0].(map[string]any)["message"].(string)
	if !strings.Contains(msg, "noSuchFieldOnViewer") {
		t.Fatalf("the error does not name the field: %q", msg)
	}
}

func TestBatchingAndAuth(t *testing.T) {
	_, h := testSetup(t)
	body, _ := json.Marshal([]map[string]any{
		{"query": "query { viewer { login } }"},
		{"query": "query { __type(name: \"SearchType\") { enumValues { name } } }"},
	})
	req := httptest.NewRequest("POST", "/api/graphql", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("batch HTTP %d: %s", rec.Code, rec.Body.String())
	}
	var batch []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &batch); err != nil || len(batch) != 2 {
		t.Fatalf("batch response is wrong: %s", rec.Body.String())
	}
	if batch[0]["data"].(map[string]any)["viewer"].(map[string]any)["login"] != "adaiversen" {
		t.Fatal("batch item 0 is wrong")
	}

	// No token is a 401.
	req = httptest.NewRequest("POST", "/api/graphql", bytes.NewReader([]byte(`{"query":"query{viewer{login}}}"}`)))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("no token: HTTP %d, want 401", rec.Code)
	}
}
