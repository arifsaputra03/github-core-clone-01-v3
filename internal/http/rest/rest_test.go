// Package rest is the forge's GitHub REST v3 API (see types.go).
package rest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"forge/internal/service"
	"forge/internal/store/embedded"
)

// testAuth is the tests' bearer check: only "Bearer test-token" passes.
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
	h := New(svc, testAuth, func(r *http.Request) string { return "http://test" })
	return svc, h
}

// call performs one request against the handler, with the test token.
func call(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// callNoAuth performs one request without a token.
func callNoAuth(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("response is not JSON (HTTP %d): %s", rec.Code, rec.Body.String())
	}
	return v
}

func asObj(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	v, ok := decode(t, rec).(map[string]any)
	if !ok {
		t.Fatalf("response is not a JSON object (HTTP %d): %s", rec.Code, rec.Body.String())
	}
	return v
}

func asArr(t *testing.T, rec *httptest.ResponseRecorder) []any {
	t.Helper()
	v, ok := decode(t, rec).([]any)
	if !ok {
		t.Fatalf("response is not a JSON array (HTTP %d): %s", rec.Code, rec.Body.String())
	}
	return v
}

func mustStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("HTTP %d, want %d: %s", rec.Code, want, rec.Body.String())
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestRootAndMeta(t *testing.T) {
	_, h := testSetup(t)
	rec := call(t, h, "GET", "/api/v3/", nil)
	mustStatus(t, rec, 200)
	if rec.Header().Get("X-OAuth-Scopes") == "" {
		t.Fatal("GET /api/v3/ carries no X-OAuth-Scopes header")
	}
	rec = call(t, h, "GET", "/api/v3/meta", nil)
	mustStatus(t, rec, 200)
	if asObj(t, rec)["installed_version"] == "" {
		t.Fatal("GET /api/v3/meta has an empty installed_version")
	}
	// The root needs no token; the other routes do.
	rec = callNoAuth(t, h, "GET", "/api/v3/")
	mustStatus(t, rec, 200)
	rec = callNoAuth(t, h, "GET", "/api/v3/meta")
	mustStatus(t, rec, 401)
}

func TestUser(t *testing.T) {
	_, h := testSetup(t)
	rec := call(t, h, "GET", "/api/v3/user", nil)
	mustStatus(t, rec, 200)
	if got := asObj(t, rec)["login"]; got != "adaiversen" {
		t.Fatalf("GET /user login = %v, want adaiversen", got)
	}
}

func TestReposAndContents(t *testing.T) {
	_, h := testSetup(t)

	rec := call(t, h, "POST", "/api/v3/user/repos", map[string]any{"name": "t1", "private": true})
	mustStatus(t, rec, 201)
	repo := asObj(t, rec)
	if repo["name"] != "t1" || repo["private"] != true || repo["default_branch"] == "" {
		t.Fatalf("created repo is wrong: %v", repo)
	}
	// A duplicate name is a 422.
	rec = call(t, h, "POST", "/api/v3/user/repos", map[string]any{"name": "t1"})
	mustStatus(t, rec, 422)

	rec = call(t, h, "GET", "/api/v3/repos/adaiversen/t1", nil)
	mustStatus(t, rec, 200)
	rec = call(t, h, "GET", "/api/v3/repos/other/t1", nil)
	mustStatus(t, rec, 404)
	rec = call(t, h, "GET", "/api/v3/repos/adaiversen/missing", nil)
	mustStatus(t, rec, 404)

	rec = call(t, h, "PATCH", "/api/v3/repos/adaiversen/t1", map[string]any{"description": "hello"})
	mustStatus(t, rec, 200)
	if asObj(t, rec)["description"] != "hello" {
		t.Fatal("PATCH did not change the description")
	}

	// The first contents write is the root commit on the default branch.
	rec = call(t, h, "PUT", "/api/v3/repos/adaiversen/t1/contents/README.md",
		map[string]any{"message": "first commit", "content": b64("hello world\n")})
	mustStatus(t, rec, 201)
	put := asObj(t, rec)
	commitSHA := put["commit"].(map[string]any)["sha"].(string)
	blobSHA := put["content"].(map[string]any)["sha"].(string)
	if commitSHA == "" || blobSHA == "" {
		t.Fatalf("PUT contents returned no shas: %v", put)
	}

	rec = call(t, h, "GET", "/api/v3/repos/adaiversen/t1/contents/README.md", nil)
	mustStatus(t, rec, 200)
	got := asObj(t, rec)
	if got["type"] != "file" || got["encoding"] != "base64" || got["sha"] != blobSHA {
		t.Fatalf("GET contents is wrong: %v", got)
	}
	content, _ := base64.StdEncoding.DecodeString(got["content"].(string))
	if string(content) != "hello world\n" {
		t.Fatalf("GET contents body = %q", content)
	}

	// A directory read lists its entries.
	rec = call(t, h, "PUT", "/api/v3/repos/adaiversen/t1/contents/docs/guide.txt",
		map[string]any{"message": "docs", "content": b64("guide\n")})
	mustStatus(t, rec, 201)
	rec = call(t, h, "GET", "/api/v3/repos/adaiversen/t1/contents/docs", nil)
	mustStatus(t, rec, 200)
	entries := asArr(t, rec)
	if len(entries) != 1 || entries[0].(map[string]any)["type"] != "file" ||
		entries[0].(map[string]any)["name"] != "guide.txt" {
		t.Fatalf("GET contents/docs is wrong: %v", entries)
	}

	// Branches and commits.
	rec = call(t, h, "POST", "/api/v3/repos/adaiversen/t1/git/refs",
		map[string]any{"ref": "refs/heads/feature", "sha": commitSHA})
	mustStatus(t, rec, 201)
	if asObj(t, rec)["ref"] != "refs/heads/feature" {
		t.Fatal("POST /git/refs returned the wrong ref")
	}
	rec = call(t, h, "POST", "/api/v3/repos/adaiversen/t1/git/refs",
		map[string]any{"ref": "refs/heads/feature", "sha": commitSHA})
	mustStatus(t, rec, 422)

	rec = call(t, h, "GET", "/api/v3/repos/adaiversen/t1/branches", nil)
	mustStatus(t, rec, 200)
	branches := asArr(t, rec)
	if len(branches) != 2 {
		t.Fatalf("GET /branches returned %d branches, want 2", len(branches))
	}
	rec = call(t, h, "GET", "/api/v3/repos/adaiversen/t1/git/refs/heads/feature", nil)
	mustStatus(t, rec, 200)
	if asObj(t, rec)["object"].(map[string]any)["sha"] != commitSHA {
		t.Fatal("GET /git/refs/heads/feature returned the wrong sha")
	}
	rec = call(t, h, "GET", "/api/v3/repos/adaiversen/t1/commits", nil)
	mustStatus(t, rec, 200)
	commits := asArr(t, rec)
	if len(commits) != 2 ||
		commits[0].(map[string]any)["commit"].(map[string]any)["message"] != "docs" ||
		commits[1].(map[string]any)["commit"].(map[string]any)["message"] != "first commit" {
		t.Fatalf("GET /commits is wrong: %v", commits)
	}

	// A stale sha on an update is a 409; a missing file is a 404.
	rec = call(t, h, "PUT", "/api/v3/repos/adaiversen/t1/contents/README.md",
		map[string]any{"message": "stale", "content": b64("x"), "sha": "0000000000000000000000000000000000000000"})
	mustStatus(t, rec, 409)
	rec = call(t, h, "GET", "/api/v3/repos/adaiversen/t1/contents/nope.txt", nil)
	mustStatus(t, rec, 404)

	// DELETE the repo: a later GET is 404.
	rec = call(t, h, "DELETE", "/api/v3/repos/adaiversen/t1", nil)
	mustStatus(t, rec, 204)
	rec = call(t, h, "GET", "/api/v3/repos/adaiversen/t1", nil)
	mustStatus(t, rec, 404)
}

func TestIssuesLabelsComments(t *testing.T) {
	_, h := testSetup(t)
	call(t, h, "POST", "/api/v3/user/repos", map[string]any{"name": "t2"})

	rec := call(t, h, "POST", "/api/v3/repos/adaiversen/t2/labels",
		map[string]any{"name": "bug", "color": "d73a4a"})
	mustStatus(t, rec, 201)
	if asObj(t, rec)["name"] != "bug" || asObj(t, rec)["color"] != "d73a4a" {
		t.Fatalf("label is wrong: %v", asObj(t, rec))
	}
	rec = call(t, h, "POST", "/api/v3/repos/adaiversen/t2/labels",
		map[string]any{"name": "bug", "color": "d73a4a"})
	mustStatus(t, rec, 422)

	rec = call(t, h, "POST", "/api/v3/repos/adaiversen/t2/issues",
		map[string]any{"title": "First", "body": "body", "labels": []string{"bug"}})
	mustStatus(t, rec, 201)
	issue := asObj(t, rec)
	if issue["number"] != float64(1) || issue["state"] != "open" {
		t.Fatalf("issue is wrong: %v", issue)
	}
	if labels := issue["labels"].([]any); len(labels) != 1 || labels[0].(map[string]any)["name"] != "bug" {
		t.Fatalf("issue labels are wrong: %v", labels)
	}

	// An unknown label is a 422.
	rec = call(t, h, "POST", "/api/v3/repos/adaiversen/t2/issues",
		map[string]any{"title": "Bad", "labels": []string{"nope"}})
	mustStatus(t, rec, 422)

	rec = call(t, h, "POST", "/api/v3/repos/adaiversen/t2/issues",
		map[string]any{"title": "Second"})
	mustStatus(t, rec, 201)

	rec = call(t, h, "GET", "/api/v3/repos/adaiversen/t2/issues?state=open", nil)
	mustStatus(t, rec, 200)
	if len(asArr(t, rec)) != 2 {
		t.Fatalf("GET /issues?state=open returned %d, want 2", len(asArr(t, rec)))
	}
	rec = call(t, h, "GET", "/api/v3/repos/adaiversen/t2/issues?state=closed", nil)
	mustStatus(t, rec, 200)
	if len(asArr(t, rec)) != 0 {
		t.Fatalf("GET /issues?state=closed returned %d, want 0", len(asArr(t, rec)))
	}

	rec = call(t, h, "PATCH", "/api/v3/repos/adaiversen/t2/issues/1",
		map[string]any{"state": "closed"})
	mustStatus(t, rec, 200)
	if asObj(t, rec)["state"] != "closed" {
		t.Fatal("PATCH did not close the issue")
	}
	rec = call(t, h, "GET", "/api/v3/repos/adaiversen/t2/issues/99", nil)
	mustStatus(t, rec, 404)

	rec = call(t, h, "POST", "/api/v3/repos/adaiversen/t2/issues/1/comments",
		map[string]any{"body": "hello"})
	mustStatus(t, rec, 201)
	if asObj(t, rec)["body"] != "hello" {
		t.Fatalf("comment is wrong: %v", asObj(t, rec))
	}
	rec = call(t, h, "GET", "/api/v3/repos/adaiversen/t2/issues/1/comments", nil)
	mustStatus(t, rec, 200)
	if len(asArr(t, rec)) != 1 {
		t.Fatalf("GET comments returned %d, want 1", len(asArr(t, rec)))
	}

	// Labels: patch and delete.
	rec = call(t, h, "PATCH", "/api/v3/repos/adaiversen/t2/labels/bug",
		map[string]any{"color": "000000"})
	mustStatus(t, rec, 200)
	if asObj(t, rec)["color"] != "000000" {
		t.Fatal("PATCH did not recolour the label")
	}
	rec = call(t, h, "DELETE", "/api/v3/repos/adaiversen/t2/labels/bug", nil)
	mustStatus(t, rec, 204)
	rec = call(t, h, "GET", "/api/v3/repos/adaiversen/t2/labels", nil)
	mustStatus(t, rec, 200)
	if len(asArr(t, rec)) != 0 {
		t.Fatal("DELETE did not remove the label")
	}
}

func TestPullRequestsAndMilestones(t *testing.T) {
	_, h := testSetup(t)
	call(t, h, "POST", "/api/v3/user/repos", map[string]any{"name": "t3"})
	rec := call(t, h, "PUT", "/api/v3/repos/adaiversen/t3/contents/a.txt",
		map[string]any{"message": "base", "content": b64("one\n")})
	mustStatus(t, rec, 201)
	baseSHA := asObj(t, rec)["commit"].(map[string]any)["sha"].(string)

	rec = call(t, h, "POST", "/api/v3/repos/adaiversen/t3/git/refs",
		map[string]any{"ref": "refs/heads/feature", "sha": baseSHA})
	mustStatus(t, rec, 201)
	rec = call(t, h, "PUT", "/api/v3/repos/adaiversen/t3/contents/b.txt",
		map[string]any{"message": "feature work", "content": b64("two\n"), "branch": "feature"})
	mustStatus(t, rec, 201)
	featureSHA := asObj(t, rec)["commit"].(map[string]any)["sha"].(string)

	rec = call(t, h, "POST", "/api/v3/repos/adaiversen/t3/pulls",
		map[string]any{"title": "Add b", "head": "feature", "base": "main"})
	mustStatus(t, rec, 201)
	pr := asObj(t, rec)
	if pr["number"] != float64(1) || pr["state"] != "open" {
		t.Fatalf("pull request is wrong: %v", pr)
	}
	if pr["head"].(map[string]any)["sha"] != featureSHA {
		t.Fatalf("head sha is %v, want %s", pr["head"], featureSHA)
	}
	if pr["base"].(map[string]any)["sha"] != baseSHA {
		t.Fatalf("base sha is %v, want %s", pr["base"], baseSHA)
	}
	// A duplicate open pull request from the same head is a 422.
	rec = call(t, h, "POST", "/api/v3/repos/adaiversen/t3/pulls",
		map[string]any{"title": "Again", "head": "feature", "base": "main"})
	mustStatus(t, rec, 422)

	rec = call(t, h, "GET", "/api/v3/repos/adaiversen/t3/pulls/1/files", nil)
	mustStatus(t, rec, 200)
	files := asArr(t, rec)
	if len(files) != 1 {
		t.Fatalf("GET /pulls/1/files returned %d files, want 1", len(files))
	}
	file := files[0].(map[string]any)
	if file["filename"] != "b.txt" || file["status"] != "added" || file["additions"] != float64(1) {
		t.Fatalf("pull file is wrong: %v", file)
	}

	rec = call(t, h, "PUT", "/api/v3/repos/adaiversen/t3/pulls/1/merge",
		map[string]any{"merge_method": "squash"})
	mustStatus(t, rec, 200)
	if asObj(t, rec)["merged"] != true {
		t.Fatalf("merge did not report merged:true: %v", asObj(t, rec))
	}
	rec = call(t, h, "GET", "/api/v3/repos/adaiversen/t3/pulls/1", nil)
	mustStatus(t, rec, 200)
	if asObj(t, rec)["merged"] != true || asObj(t, rec)["state"] != "closed" {
		t.Fatalf("merged pull request is wrong: %v", asObj(t, rec))
	}
	// Merging again is a 405.
	rec = call(t, h, "PUT", "/api/v3/repos/adaiversen/t3/pulls/1/merge",
		map[string]any{"merge_method": "squash"})
	mustStatus(t, rec, 405)
	// The merge commit put b.txt on the default branch.
	rec = call(t, h, "GET", "/api/v3/repos/adaiversen/t3/contents/b.txt", nil)
	mustStatus(t, rec, 200)

	// Milestones.
	rec = call(t, h, "POST", "/api/v3/repos/adaiversen/t3/milestones",
		map[string]any{"title": "M1", "description": "first"})
	mustStatus(t, rec, 201)
	if asObj(t, rec)["number"] != float64(1) || asObj(t, rec)["state"] != "open" {
		t.Fatalf("milestone is wrong: %v", asObj(t, rec))
	}
	rec = call(t, h, "GET", "/api/v3/repos/adaiversen/t3/milestones", nil)
	mustStatus(t, rec, 200)
	if len(asArr(t, rec)) != 1 {
		t.Fatalf("GET /milestones returned %d, want 1", len(asArr(t, rec)))
	}
	rec = call(t, h, "PATCH", "/api/v3/repos/adaiversen/t3/milestones/1",
		map[string]any{"state": "closed"})
	mustStatus(t, rec, 200)
	if asObj(t, rec)["state"] != "closed" {
		t.Fatal("PATCH did not close the milestone")
	}
	rec = call(t, h, "DELETE", "/api/v3/repos/adaiversen/t3/milestones/1", nil)
	mustStatus(t, rec, 204)
}

func TestMalformedJSONAndUnknownRoute(t *testing.T) {
	_, h := testSetup(t)
	req := httptest.NewRequest("POST", "/api/v3/user/repos", bytes.NewReader([]byte("{not json")))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	mustStatus(t, rec, 400)

	rec = call(t, h, "GET", "/api/v3/nope", nil)
	mustStatus(t, rec, 404)
	rec = callNoAuth(t, h, "GET", "/api/v3/user")
	mustStatus(t, rec, 401)
}

func TestPaginationLinkHeader(t *testing.T) {
	_, h := testSetup(t)
	for i := 0; i < 12; i++ {
		rec := call(t, h, "POST", "/api/v3/user/repos",
			map[string]any{"name": "r" + string(rune('a'+i))})
		mustStatus(t, rec, 201)
	}
	rec := call(t, h, "GET", "/api/v3/user/repos?per_page=10", nil)
	mustStatus(t, rec, 200)
	if len(asArr(t, rec)) != 10 {
		t.Fatalf("page 1 returned %d repos, want 10", len(asArr(t, rec)))
	}
	if link := rec.Header().Get("Link"); link == "" {
		t.Fatal("GET /user/repos?per_page=10 carries no Link header for page 2")
	}
}
