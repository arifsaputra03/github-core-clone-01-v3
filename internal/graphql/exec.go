// Package graphql is the forge's GraphQL API at POST /api/graphql (see
// parse.go and handler.go).
package graphql

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"forge/internal/domain"
	"forge/internal/git"
	"forge/internal/service"
	"forge/internal/store"
)

// typed is a resolved object value: its runtime type name and the source
// the field resolvers read from.
type typed struct {
	typ string
	val any
}

// payload is the source of a mutation payload object: the fields the
// mutation returns, keyed by field name.
type payload struct {
	fields map[string]any
}

// refValue is the source of a Ref: a branch name and its tip commit.
type refValue struct {
	name string
	oid  string
}

// pageInfoValue is the source of a PageInfo.
type pageInfoValue struct {
	hasNext bool
	hasPrev bool
	start   string
	end     string
}

// issueConn is the source of an IssueConnection.
type issueConn struct {
	nodes []*domain.Issue
	page  *pageInfoValue
	total int
}

// prConn is the source of a PullRequestConnection.
type prConn struct {
	nodes []*domain.PullRequest
	page  *pageInfoValue
	total int
}

// fileConn is the source of a PullRequestFileConnection.
type fileConn struct {
	nodes []git.FileChange
	page  *pageInfoValue
	total int
}

// typeInfo is the source of a __Type: the name of a schema type.
type typeInfo struct{ name string }

// fieldInfo is the source of a __Field: a field of a schema type.
type fieldInfo struct {
	typeName  string
	fieldName string
}

// enumValueInfo is the source of an __EnumValue.
type enumValueInfo struct{ name string }

// schemaMarker is the source of a __Schema.
type schemaMarker struct{}

// executor runs one GraphQL operation against the service.
type executor struct {
	svc       *service.Service
	vars      map[string]any
	viewer    string
	fragments map[string]*selection
}

// newExecutor builds an executor for one request: the service, the
// token's login, the request's variables and the document's fragments.
func newExecutor(svc *service.Service, viewer string, vars map[string]any, fragments map[string]*selection) *executor {
	if vars == nil {
		vars = map[string]any{}
	}
	if fragments == nil {
		fragments = map[string]*selection{}
	}
	return &executor{svc: svc, viewer: viewer, vars: vars, fragments: fragments}
}

// run validates and executes one operation, returning the response object.
func (e *executor) run(op *operation) map[string]any {
	if errs := e.validate(op); len(errs) > 0 {
		return map[string]any{"data": nil, "errors": errs}
	}
	root := "Query"
	if op.typ == "mutation" {
		root = "Mutation"
	}
	out := map[string]any{}
	var errs []map[string]any
	e.resolveSelections(root, nil, op.sels, out, &errs)
	resp := map[string]any{"data": out}
	if len(errs) > 0 {
		resp["errors"] = errs
	}
	return resp
}

// validate reports the unknown fields of an operation, in GraphQL's
// wording. A document with a validation error is not executed.
func (e *executor) validate(op *operation) []map[string]any {
	var errs []map[string]any
	root := "Query"
	if op.typ == "mutation" {
		root = "Mutation"
	}
	e.validateSelections(root, op.sels, &errs)
	return errs
}

func (e *executor) validateSelections(typeName string, sels []*selection, errs *[]map[string]any) {
	for _, sel := range sels {
		if sel.fragName != "" {
			frag, ok := e.frag(sel.fragName)
			if !ok {
				*errs = append(*errs, map[string]any{"message": fmt.Sprintf("Unknown fragment %q.", sel.fragName)})
				continue
			}
			e.validateSelections(frag.typeCond, frag.children, errs)
			continue
		}
		if sel.typeCond != "" {
			if _, ok := schema[sel.typeCond]; !ok {
				*errs = append(*errs, map[string]any{"message": fmt.Sprintf("Unknown type %q.", sel.typeCond)})
				continue
			}
			e.validateSelections(sel.typeCond, sel.children, errs)
			continue
		}
		if sel.name == "__typename" || sel.name == "__type" || sel.name == "__schema" {
			continue
		}
		child := fieldType(typeName, sel.name)
		if child == "" {
			*errs = append(*errs, map[string]any{"message": fmt.Sprintf("Cannot query field %q on type %q.", sel.name, typeName)})
			continue
		}
		if len(sel.children) > 0 {
			e.validateSelections(child, sel.children, errs)
		}
	}
}

func (e *executor) frag(name string) (*selection, bool) {
	// The fragments are held by the document; the executor keeps a copy.
	f, ok := e.fragments[name]
	return f, ok
}

// resolveValue turns a parsed value literal into a Go value, resolving
// variables from the request.
func (e *executor) resolveValue(v *value) any {
	if v == nil {
		return nil
	}
	switch v.kind {
	case "null":
		return nil
	case "bool":
		return v.b
	case "int":
		return v.i
	case "float":
		return v.f
	case "string", "enum":
		return v.s
	case "var":
		if val, ok := e.vars[v.s]; ok {
			return val
		}
		return nil
	case "list":
		out := make([]any, 0, len(v.list))
		for _, item := range v.list {
			out = append(out, e.resolveValue(item))
		}
		return out
	case "object":
		out := map[string]any{}
		for k, item := range v.obj {
			out[k] = e.resolveValue(item)
		}
		return out
	}
	return nil
}

// arg returns a resolved argument value, or nil when the argument is
// absent.
func (e *executor) arg(sel *selection, name string) any {
	if sel.args == nil {
		return nil
	}
	v, ok := sel.args[name]
	if !ok {
		return nil
	}
	return e.resolveValue(v)
}

// asInt coerces a resolved value to an int (JSON numbers arrive as
// float64, literals as int64).
func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	}
	return 0
}

// asString coerces a resolved value to a string.
func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// asStringList coerces a resolved value to a list of strings.
func asStringList(v any) []string {
	list, ok := v.([]any)
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

// asMap coerces a resolved value to a map (an input object).
func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// skip reports whether a selection's @skip/@include directives hide it.
func (e *executor) skip(sel *selection) bool {
	for _, d := range sel.directives {
		ifV := e.resolveValue(d.args["if"])
		b, _ := ifV.(bool)
		if d.name == "skip" && b {
			return true
		}
		if d.name == "include" && !b {
			return true
		}
	}
	return false
}

// resolveSelections resolves a selection set against a source of the given
// runtime type, filling out. Field errors are collected and the field is
// set to null, as GraphQL requires.
func (e *executor) resolveSelections(typeName string, src any, sels []*selection, out map[string]any, errs *[]map[string]any) {
	for _, sel := range sels {
		if e.skip(sel) {
			continue
		}
		if sel.fragName != "" {
			frag, ok := e.frag(sel.fragName)
			if !ok {
				continue
			}
			if frag.typeCond != "" && frag.typeCond != typeName {
				continue
			}
			e.resolveSelections(typeName, src, frag.children, out, errs)
			continue
		}
		if sel.typeCond != "" {
			if sel.typeCond != typeName {
				continue
			}
			e.resolveSelections(typeName, src, sel.children, out, errs)
			continue
		}
		if sel.name == "__typename" {
			out[sel.alias] = typeName
			continue
		}
		if sel.name == "__type" {
			// A meta-field: valid on every type.
			name := asString(e.arg(sel, "name"))
			var val any
			if _, ok := schema[name]; ok {
				val = typed{typ: "__Type", val: typeInfo{name: name}}
			}
			if len(sel.children) == 0 {
				out[sel.alias] = scalarOf(val)
			} else {
				out[sel.alias] = e.resolveChildren(val, sel.children, errs)
			}
			continue
		}
		if sel.name == "__schema" {
			val := typed{typ: "__Schema", val: schemaMarker{}}
			if len(sel.children) == 0 {
				out[sel.alias] = scalarOf(val)
			} else {
				out[sel.alias] = e.resolveChildren(val, sel.children, errs)
			}
			continue
		}
		val, err := e.resolveField(typeName, src, sel)
		if err != nil {
			*errs = append(*errs, map[string]any{"message": err.Error(), "path": []any{sel.alias}})
			out[sel.alias] = nil
			continue
		}
		if len(sel.children) == 0 {
			out[sel.alias] = scalarOf(val)
			continue
		}
		out[sel.alias] = e.resolveChildren(val, sel.children, errs)
	}
}

// resolveChildren resolves a field's sub-selections against its value: a
// single object, or a list of objects.
func (e *executor) resolveChildren(val any, children []*selection, errs *[]map[string]any) any {
	switch v := val.(type) {
	case nil:
		return nil
	case typed:
		out := map[string]any{}
		e.resolveSelections(v.typ, v.val, children, out, errs)
		return out
	case []typed:
		out := make([]any, 0, len(v))
		for _, item := range v {
			child := map[string]any{}
			e.resolveSelections(item.typ, item.val, children, child, errs)
			out = append(out, child)
		}
		return out
	default:
		return scalarOf(val)
	}
}

// scalarOf unwraps a resolved value to the JSON scalar it renders as.
func scalarOf(val any) any {
	switch v := val.(type) {
	case nil:
		return nil
	case typed:
		return nil
	case []typed:
		return nil
	default:
		return v
	}
}

// resolveField resolves one field on a source. It returns a scalar, a
// typed object, or a []typed list.
func (e *executor) resolveField(typeName string, src any, sel *selection) (any, error) {
	switch typeName {
	case "Query":
		return e.resolveQuery(sel)
	case "Mutation":
		return e.resolveMutation(sel)
	case "User":
		return e.resolveUser(src.(*domain.User), sel)
	case "Repository":
		return e.resolveRepository(src.(*domain.Repository), sel)
	case "Ref":
		return e.resolveRef(src.(refValue), sel)
	case "GitObject":
		return e.resolveGitObject(src.(refValue), sel)
	case "Issue":
		return e.resolveIssue(src.(*domain.Issue), sel)
	case "IssueConnection":
		return e.resolveIssueConn(src.(issueConn), sel)
	case "IssueEdge":
		return e.resolveIssueEdge(src.(issueEdge), sel)
	case "IssueComment":
		return e.resolveComment(src.(*domain.Comment), sel)
	case "IssueCommentEdge":
		return e.resolveCommentEdge(src.(commentEdge), sel)
	case "PageInfo":
		return e.resolvePageInfo(src.(pageInfoValue), sel)
	case "PullRequest":
		return e.resolvePullRequest(src.(*domain.PullRequest), sel)
	case "PullRequestConnection":
		return e.resolvePullConn(src.(prConn), sel)
	case "PullRequestEdge":
		return e.resolvePullEdge(src.(pullEdge), sel)
	case "PullRequestFileConnection":
		return e.resolveFileConn(src.(fileConn), sel)
	case "PullRequestFileEdge":
		return e.resolveFileEdge(src.(fileEdge), sel)
	case "PullRequestFile":
		return e.resolveFile(src.(git.FileChange), sel)
	case "__Type":
		return e.resolveType(src.(typeInfo), sel)
	case "__Field":
		return e.resolveFieldInfo(src.(fieldInfo), sel)
	case "__EnumValue":
		return e.resolveEnumValue(src.(enumValueInfo), sel)
	case "__InputValue":
		return e.resolveInputValue(src.(inputValueInfo), sel)
	case "__Schema":
		return e.resolveSchema(src.(schemaMarker), sel)
	}
	// Payload objects are wrapped in a payload source.
	if p, ok := src.(payload); ok {
		if v, ok := p.fields[sel.name]; ok {
			return v, nil
		}
		return nil, nil
	}
	return nil, fmt.Errorf("graphql: cannot resolve a field on %q", typeName)
}

// --- Query and Mutation ---

func (e *executor) resolveQuery(sel *selection) (any, error) {
	switch sel.name {
	case "viewer":
		u, err := e.svc.GetUser(e.viewer)
		if err != nil {
			return nil, err
		}
		return typed{typ: "User", val: u}, nil
	case "repository":
		owner := asString(e.arg(sel, "owner"))
		name := asString(e.arg(sel, "name"))
		r, err := e.svc.GetRepository(owner, name)
		if err != nil {
			if isNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return typed{typ: "Repository", val: r}, nil
	case "node":
		id := asString(e.arg(sel, "id"))
		kind, numeric, err := service.ParseGlobalID(id)
		if err != nil {
			return nil, nil
		}
		switch kind {
		case "Repository":
			r, err := e.svc.RepositoryByID(numeric)
			if err != nil {
				return nil, nil
			}
			return typed{typ: "Repository", val: r}, nil
		case "Issue":
			issue, err := e.svc.IssueByID(numeric)
			if err != nil {
				return nil, nil
			}
			return typed{typ: "Issue", val: issue}, nil
		case "PullRequest":
			pr, err := e.svc.PullByID(numeric)
			if err != nil {
				return nil, nil
			}
			return typed{typ: "PullRequest", val: pr}, nil
		case "IssueComment":
			c, err := e.svc.CommentByID(numeric)
			if err != nil {
				return nil, nil
			}
			return typed{typ: "IssueComment", val: c}, nil
		}
		return nil, nil
	case "__type":
		name := asString(e.arg(sel, "name"))
		if _, ok := schema[name]; !ok {
			return nil, nil
		}
		return typed{typ: "__Type", val: typeInfo{name: name}}, nil
	case "__schema":
		return typed{typ: "__Schema", val: schemaMarker{}}, nil
	}
	return nil, fmt.Errorf("graphql: unknown Query field %q", sel.name)
}

func (e *executor) resolveMutation(sel *selection) (any, error) {
	switch sel.name {
	case "createIssue":
		in := asMap(e.arg(sel, "input"))
		repoID := asString(in["repositoryId"])
		kind, numeric, err := service.ParseGlobalID(repoID)
		if err != nil || kind != "Repository" {
			return nil, fmt.Errorf("graphql: bad repositoryId %q", repoID)
		}
		repo, err := e.svc.RepositoryByID(numeric)
		if err != nil {
			return nil, err
		}
		issue, err := e.svc.CreateIssue(repo.Owner, repo.Name, service.CreateIssueInput{
			Title:  asString(in["title"]),
			Body:   asString(in["body"]),
			Author: e.viewer,
		})
		if err != nil {
			return nil, err
		}
		return typed{typ: "CreateIssuePayload", val: payload{fields: map[string]any{"issue": typed{typ: "Issue", val: issue}}}}, nil
	case "closeIssue":
		in := asMap(e.arg(sel, "input"))
		issueID := asString(in["issueId"])
		kind, numeric, err := service.ParseGlobalID(issueID)
		if err != nil || kind != "Issue" {
			return nil, fmt.Errorf("graphql: bad issueId %q", issueID)
		}
		issue, err := e.svc.IssueByID(numeric)
		if err != nil {
			return nil, err
		}
		closed := "closed"
		issue, err = e.svc.UpdateIssue(issue.RepoOwner, issue.RepoName, issue.Number, service.UpdateIssueInput{State: &closed})
		if err != nil {
			return nil, err
		}
		return typed{typ: "CloseIssuePayload", val: payload{fields: map[string]any{"issue": typed{typ: "Issue", val: issue}}}}, nil
	case "addComment":
		in := asMap(e.arg(sel, "input"))
		subjectID := asString(in["subjectId"])
		kind, numeric, err := service.ParseGlobalID(subjectID)
		if err != nil || kind != "Issue" {
			return nil, fmt.Errorf("graphql: bad subjectId %q", subjectID)
		}
		issue, err := e.svc.IssueByID(numeric)
		if err != nil {
			return nil, err
		}
		comment, err := e.svc.CreateComment(issue.RepoOwner, issue.RepoName, issue.Number, asString(in["body"]), e.viewer)
		if err != nil {
			return nil, err
		}
		return typed{typ: "AddCommentPayload", val: payload{fields: map[string]any{"commentEdge": typed{typ: "IssueCommentEdge", val: commentEdge{node: comment}}}}}, nil
	case "createPullRequest":
		in := asMap(e.arg(sel, "input"))
		repoID := asString(in["repositoryId"])
		kind, numeric, err := service.ParseGlobalID(repoID)
		if err != nil || kind != "Repository" {
			return nil, fmt.Errorf("graphql: bad repositoryId %q", repoID)
		}
		repo, err := e.svc.RepositoryByID(numeric)
		if err != nil {
			return nil, err
		}
		pr, err := e.svc.CreatePull(repo.Owner, repo.Name, service.CreatePullInput{
			Title:  asString(in["title"]),
			Body:   asString(in["body"]),
			Head:   asString(in["headRefName"]),
			Base:   asString(in["baseRefName"]),
			Author: e.viewer,
		})
		if err != nil {
			return nil, err
		}
		return typed{typ: "CreatePullRequestPayload", val: payload{fields: map[string]any{"pullRequest": typed{typ: "PullRequest", val: pr}}}}, nil
	case "mergePullRequest":
		in := asMap(e.arg(sel, "input"))
		prID := asString(in["pullRequestId"])
		kind, numeric, err := service.ParseGlobalID(prID)
		if err != nil || kind != "PullRequest" {
			return nil, fmt.Errorf("graphql: bad pullRequestId %q", prID)
		}
		pr, err := e.svc.PullByID(numeric)
		if err != nil {
			return nil, err
		}
		method := asString(in["mergeMethod"])
		if method == "" {
			method = "squash"
		}
		if _, err := e.svc.MergePull(pr.RepoOwner, pr.RepoName, pr.Number, method); err != nil {
			return nil, err
		}
		pr, err = e.svc.GetPull(pr.RepoOwner, pr.RepoName, pr.Number)
		if err != nil {
			return nil, err
		}
		return typed{typ: "MergePullRequestPayload", val: payload{fields: map[string]any{"pullRequest": typed{typ: "PullRequest", val: pr}}}}, nil
	}
	return nil, fmt.Errorf("graphql: unknown Mutation field %q", sel.name)
}

// --- Object resolvers ---

func (e *executor) resolveUser(u *domain.User, sel *selection) (any, error) {
	switch sel.name {
	case "id":
		return service.GlobalID("User", u.ID), nil
	case "login":
		return u.Login, nil
	case "name":
		return u.Name, nil
	case "email":
		return u.Email, nil
	case "avatarUrl":
		return u.AvatarURL, nil
	}
	return nil, fmt.Errorf("graphql: unknown User field %q", sel.name)
}

func (e *executor) resolveRepository(r *domain.Repository, sel *selection) (any, error) {
	switch sel.name {
	case "id":
		return service.GlobalID("Repository", r.ID), nil
	case "databaseId":
		return int(r.ID), nil
	case "name":
		return r.Name, nil
	case "nameWithOwner":
		return r.Owner + "/" + r.Name, nil
	case "owner":
		u, err := e.svc.GetUser(r.Owner)
		if err != nil {
			u = &domain.User{Login: r.Owner}
		}
		return typed{typ: "User", val: u}, nil
	case "defaultBranchRef":
		branch, err := e.svc.DefaultBranch(r.Owner, r.Name)
		if err != nil || branch == "" {
			return nil, nil
		}
		oid := ""
		if b, err := e.svc.GetBranch(r.Owner, r.Name, branch); err == nil {
			oid = b.SHA
		}
		return typed{typ: "Ref", val: refValue{name: branch, oid: oid}}, nil
	case "viewerPermission":
		return "ADMIN", nil
	case "hasIssuesEnabled":
		return true, nil
	case "issue":
		number := asInt(e.arg(sel, "number"))
		issue, err := e.svc.GetIssue(r.Owner, r.Name, number)
		if err != nil {
			if isNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return typed{typ: "Issue", val: issue}, nil
	case "issues":
		return e.resolveRepositoryIssues(r, sel)
	case "pullRequest":
		number := asInt(e.arg(sel, "number"))
		pr, err := e.svc.GetPull(r.Owner, r.Name, number)
		if err != nil {
			if isNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return typed{typ: "PullRequest", val: pr}, nil
	case "pullRequests":
		return e.resolveRepositoryPulls(r, sel)
	}
	return nil, fmt.Errorf("graphql: unknown Repository field %q", sel.name)
}

// resolveRepositoryIssues resolves repository.issues with the states and
// orderBy filters and Relay cursor pagination.
func (e *executor) resolveRepositoryIssues(r *domain.Repository, sel *selection) (any, error) {
	states := asStringList(e.arg(sel, "states"))
	state := "all"
	if len(states) == 1 {
		switch states[0] {
		case "OPEN":
			state = "open"
		case "CLOSED":
			state = "closed"
		}
	}
	direction := "desc"
	if order := asMap(e.arg(sel, "orderBy")); order != nil {
		if d := asString(order["direction"]); d == "ASC" || d == "DESC" {
			direction = strings.ToLower(d)
		}
	}
	issues, err := e.svc.ListIssues(r.Owner, r.Name, service.IssueListOptions{State: state, Sort: "created", Direction: direction})
	if err != nil {
		return nil, err
	}
	// ListIssues orders by (createdAt, number); for the connection the
	// number order is the same as creation order, and it is the order the
	// cursor is defined over.
	first := asInt(e.arg(sel, "first"))
	after := asString(e.arg(sel, "after"))
	page, hasNext, start, end := paginate(len(issues), first, after)
	nodes := make([]*domain.Issue, 0, len(page))
	for _, idx := range page {
		nodes = append(nodes, issues[idx])
	}
	return typed{typ: "IssueConnection", val: issueConn{nodes: nodes, page: &pageInfoValue{hasNext: hasNext, start: start, end: end}, total: len(issues)}}, nil
}

// resolveRepositoryPulls resolves repository.pullRequests.
func (e *executor) resolveRepositoryPulls(r *domain.Repository, sel *selection) (any, error) {
	states := asStringList(e.arg(sel, "states"))
	state := "open"
	if len(states) == 0 {
		state = "all"
	} else if len(states) == 1 {
		switch states[0] {
		case "OPEN":
			state = "open"
		case "CLOSED", "MERGED":
			state = "closed"
		}
	}
	pulls, err := e.svc.ListPulls(r.Owner, r.Name, state)
	if err != nil {
		return nil, err
	}
	first := asInt(e.arg(sel, "first"))
	after := asString(e.arg(sel, "after"))
	page, hasNext, start, end := paginate(len(pulls), first, after)
	nodes := make([]*domain.PullRequest, 0, len(page))
	for _, idx := range page {
		nodes = append(nodes, pulls[idx])
	}
	return typed{typ: "PullRequestConnection", val: prConn{nodes: nodes, page: &pageInfoValue{hasNext: hasNext, start: start, end: end}, total: len(pulls)}}, nil
}

func (e *executor) resolveRef(ref refValue, sel *selection) (any, error) {
	switch sel.name {
	case "name":
		return ref.name, nil
	case "target":
		return typed{typ: "GitObject", val: ref}, nil
	}
	return nil, fmt.Errorf("graphql: unknown Ref field %q", sel.name)
}

func (e *executor) resolveGitObject(ref refValue, sel *selection) (any, error) {
	if sel.name == "oid" {
		return ref.oid, nil
	}
	return nil, fmt.Errorf("graphql: unknown GitObject field %q", sel.name)
}

func (e *executor) resolveIssue(issue *domain.Issue, sel *selection) (any, error) {
	switch sel.name {
	case "id":
		return service.GlobalID("Issue", issue.ID), nil
	case "number":
		return issue.Number, nil
	case "title":
		return issue.Title, nil
	case "body":
		return issue.Body, nil
	case "state":
		if issue.State == domain.IssueClosed {
			return "CLOSED", nil
		}
		return "OPEN", nil
	case "createdAt":
		return issue.CreatedAt.UTC().Format(time.RFC3339), nil
	case "updatedAt":
		return issue.UpdatedAt.UTC().Format(time.RFC3339), nil
	case "author":
		return typed{typ: "User", val: &domain.User{Login: issue.Author}}, nil
	case "url":
		return "", nil
	}
	return nil, fmt.Errorf("graphql: unknown Issue field %q", sel.name)
}

// issueEdge is the source of an IssueEdge.
type issueEdge struct {
	node   *domain.Issue
	cursor string
}

func (e *executor) resolveIssueConn(conn issueConn, sel *selection) (any, error) {
	switch sel.name {
	case "nodes":
		out := make([]typed, 0, len(conn.nodes))
		for _, issue := range conn.nodes {
			out = append(out, typed{typ: "Issue", val: issue})
		}
		return out, nil
	case "edges":
		out := make([]typed, 0, len(conn.nodes))
		for i, issue := range conn.nodes {
			out = append(out, typed{typ: "IssueEdge", val: issueEdge{node: issue, cursor: encodeCursor(i)}})
		}
		return out, nil
	case "pageInfo":
		return typed{typ: "PageInfo", val: *conn.page}, nil
	case "totalCount":
		return conn.total, nil
	}
	return nil, fmt.Errorf("graphql: unknown IssueConnection field %q", sel.name)
}

func (e *executor) resolveIssueEdge(edge issueEdge, sel *selection) (any, error) {
	switch sel.name {
	case "node":
		return typed{typ: "Issue", val: edge.node}, nil
	case "cursor":
		return edge.cursor, nil
	}
	return nil, fmt.Errorf("graphql: unknown IssueEdge field %q", sel.name)
}

// commentEdge is the source of an IssueCommentEdge.
type commentEdge struct {
	node   *domain.Comment
	cursor string
}

func (e *executor) resolveComment(c *domain.Comment, sel *selection) (any, error) {
	switch sel.name {
	case "id":
		return service.GlobalID("IssueComment", c.ID), nil
	case "body":
		return c.Body, nil
	case "author":
		return typed{typ: "User", val: &domain.User{Login: c.Author}}, nil
	case "createdAt":
		return c.CreatedAt.UTC().Format(time.RFC3339), nil
	case "updatedAt":
		return c.UpdatedAt.UTC().Format(time.RFC3339), nil
	}
	return nil, fmt.Errorf("graphql: unknown IssueComment field %q", sel.name)
}

func (e *executor) resolveCommentEdge(edge commentEdge, sel *selection) (any, error) {
	switch sel.name {
	case "node":
		return typed{typ: "IssueComment", val: edge.node}, nil
	case "cursor":
		return edge.cursor, nil
	}
	return nil, fmt.Errorf("graphql: unknown IssueCommentEdge field %q", sel.name)
}

func (e *executor) resolvePageInfo(p pageInfoValue, sel *selection) (any, error) {
	switch sel.name {
	case "hasNextPage":
		return p.hasNext, nil
	case "hasPreviousPage":
		return p.hasPrev, nil
	case "startCursor":
		if p.start == "" {
			return nil, nil
		}
		return p.start, nil
	case "endCursor":
		if p.end == "" {
			return nil, nil
		}
		return p.end, nil
	}
	return nil, fmt.Errorf("graphql: unknown PageInfo field %q", sel.name)
}

func (e *executor) resolvePullRequest(pr *domain.PullRequest, sel *selection) (any, error) {
	switch sel.name {
	case "id":
		return service.GlobalID("PullRequest", pr.ID), nil
	case "number":
		return pr.Number, nil
	case "title":
		return pr.Title, nil
	case "body":
		return pr.Body, nil
	case "state":
		if pr.Merged {
			return "MERGED", nil
		}
		if pr.State == domain.PullClosed {
			return "CLOSED", nil
		}
		return "OPEN", nil
	case "merged":
		return pr.Merged, nil
	case "mergeable":
		return e.svc.MergeableFor(pr.RepoOwner, pr.RepoName, pr.Number), nil
	case "headRefName":
		return pr.Head, nil
	case "baseRefName":
		return pr.Base, nil
	case "headRef":
		return typed{typ: "Ref", val: refValue{name: pr.Head, oid: e.svc.HeadSHAFor(pr)}}, nil
	case "baseRef":
		return typed{typ: "Ref", val: refValue{name: pr.Base, oid: e.svc.BaseSHAFor(pr)}}, nil
	case "files":
		changes, err := e.svc.ListPullFiles(pr.RepoOwner, pr.RepoName, pr.Number)
		if err != nil {
			return nil, err
		}
		first := asInt(e.arg(sel, "first"))
		after := asString(e.arg(sel, "after"))
		page, hasNext, start, end := paginate(len(changes), first, after)
		nodes := make([]git.FileChange, 0, len(page))
		for _, idx := range page {
			nodes = append(nodes, changes[idx])
		}
		return typed{typ: "PullRequestFileConnection", val: fileConn{nodes: nodes, page: &pageInfoValue{hasNext: hasNext, start: start, end: end}, total: len(changes)}}, nil
	case "author":
		return typed{typ: "User", val: &domain.User{Login: pr.Author}}, nil
	case "createdAt":
		return pr.CreatedAt.UTC().Format(time.RFC3339), nil
	case "updatedAt":
		return pr.UpdatedAt.UTC().Format(time.RFC3339), nil
	}
	return nil, fmt.Errorf("graphql: unknown PullRequest field %q", sel.name)
}

// pullEdge is the source of a PullRequestEdge.
type pullEdge struct {
	node   *domain.PullRequest
	cursor string
}

func (e *executor) resolvePullConn(conn prConn, sel *selection) (any, error) {
	switch sel.name {
	case "nodes":
		out := make([]typed, 0, len(conn.nodes))
		for _, pr := range conn.nodes {
			out = append(out, typed{typ: "PullRequest", val: pr})
		}
		return out, nil
	case "edges":
		out := make([]typed, 0, len(conn.nodes))
		for i, pr := range conn.nodes {
			out = append(out, typed{typ: "PullRequestEdge", val: pullEdge{node: pr, cursor: encodeCursor(i)}})
		}
		return out, nil
	case "pageInfo":
		return typed{typ: "PageInfo", val: *conn.page}, nil
	case "totalCount":
		return conn.total, nil
	}
	return nil, fmt.Errorf("graphql: unknown PullRequestConnection field %q", sel.name)
}

func (e *executor) resolvePullEdge(edge pullEdge, sel *selection) (any, error) {
	switch sel.name {
	case "node":
		return typed{typ: "PullRequest", val: edge.node}, nil
	case "cursor":
		return edge.cursor, nil
	}
	return nil, fmt.Errorf("graphql: unknown PullRequestEdge field %q", sel.name)
}

// fileEdge is the source of a PullRequestFileEdge.
type fileEdge struct {
	node   git.FileChange
	cursor string
}

func (e *executor) resolveFileConn(conn fileConn, sel *selection) (any, error) {
	switch sel.name {
	case "nodes":
		out := make([]typed, 0, len(conn.nodes))
		for _, c := range conn.nodes {
			out = append(out, typed{typ: "PullRequestFile", val: c})
		}
		return out, nil
	case "edges":
		out := make([]typed, 0, len(conn.nodes))
		for i, c := range conn.nodes {
			out = append(out, typed{typ: "PullRequestFileEdge", val: fileEdge{node: c, cursor: encodeCursor(i)}})
		}
		return out, nil
	case "pageInfo":
		return typed{typ: "PageInfo", val: *conn.page}, nil
	case "totalCount":
		return conn.total, nil
	}
	return nil, fmt.Errorf("graphql: unknown PullRequestFileConnection field %q", sel.name)
}

func (e *executor) resolveFileEdge(edge fileEdge, sel *selection) (any, error) {
	switch sel.name {
	case "node":
		return typed{typ: "PullRequestFile", val: edge.node}, nil
	case "cursor":
		return edge.cursor, nil
	}
	return nil, fmt.Errorf("graphql: unknown PullRequestFileEdge field %q", sel.name)
}

func (e *executor) resolveFile(c git.FileChange, sel *selection) (any, error) {
	switch sel.name {
	case "path":
		return c.Filename, nil
	case "additions":
		return c.Additions, nil
	case "deletions":
		return c.Deletions, nil
	case "changes":
		return c.Changes, nil
	}
	return nil, fmt.Errorf("graphql: unknown PullRequestFile field %q", sel.name)
}

// --- Introspection ---

func (e *executor) resolveType(t typeInfo, sel *selection) (any, error) {
	def, ok := schema[t.name]
	if !ok {
		return nil, nil
	}
	switch sel.name {
	case "name":
		return t.name, nil
	case "kind":
		return def.kind, nil
	case "description":
		return nil, nil
	case "fields":
		out := make([]typed, 0, len(def.fields))
		for _, f := range def.fields {
			out = append(out, typed{typ: "__Field", val: fieldInfo{typeName: t.name, fieldName: f}})
		}
		return out, nil
	case "enumValues":
		out := make([]typed, 0, len(def.enumVals))
		for _, v := range def.enumVals {
			out = append(out, typed{typ: "__EnumValue", val: enumValueInfo{name: v}})
		}
		return out, nil
	case "inputFields":
		return []typed{}, nil
	}
	return nil, fmt.Errorf("graphql: unknown __Type field %q", sel.name)
}

func (e *executor) resolveFieldInfo(f fieldInfo, sel *selection) (any, error) {
	switch sel.name {
	case "name":
		return f.fieldName, nil
	case "description":
		return nil, nil
	case "type":
		return typed{typ: "__Type", val: typeInfo{name: "String"}}, nil
	case "args":
		return []typed{}, nil
	}
	return nil, fmt.Errorf("graphql: unknown __Field field %q", sel.name)
}

func (e *executor) resolveEnumValue(v enumValueInfo, sel *selection) (any, error) {
	switch sel.name {
	case "name":
		return v.name, nil
	case "description":
		return nil, nil
	}
	return nil, fmt.Errorf("graphql: unknown __EnumValue field %q", sel.name)
}

// inputValueInfo is the source of an __InputValue.
type inputValueInfo struct{ name string }

func (e *executor) resolveInputValue(v inputValueInfo, sel *selection) (any, error) {
	switch sel.name {
	case "name":
		return v.name, nil
	case "description":
		return nil, nil
	case "type":
		return typed{typ: "__Type", val: typeInfo{name: "String"}}, nil
	}
	return nil, fmt.Errorf("graphql: unknown __InputValue field %q", sel.name)
}

func (e *executor) resolveSchema(_ schemaMarker, sel *selection) (any, error) {
	switch sel.name {
	case "types":
		names := make([]string, 0, len(schema))
		for name := range schema {
			names = append(names, name)
		}
		// Sort for a deterministic introspection answer.
		for i := 1; i < len(names); i++ {
			for j := i; j > 0 && names[j] < names[j-1]; j-- {
				names[j], names[j-1] = names[j-1], names[j]
			}
		}
		out := make([]typed, 0, len(names))
		for _, name := range names {
			out = append(out, typed{typ: "__Type", val: typeInfo{name: name}})
		}
		return out, nil
	case "queryType":
		return typed{typ: "__Type", val: typeInfo{name: "Query"}}, nil
	case "mutationType":
		return typed{typ: "__Type", val: typeInfo{name: "Mutation"}}, nil
	}
	return nil, fmt.Errorf("graphql: unknown __Schema field %q", sel.name)
}

// paginate slices an ordered list of n items by first/after. The cursor is
// the base64 of "cursor:<index>" in the ordered list. It returns the
// indices of the page, whether more items follow, and the page's start and
// end cursors ("" when the page is empty).
func paginate(n, first int, after string) (page []int, hasNext bool, start, end string) {
	startIdx := 0
	if after != "" {
		if idx, ok := decodeCursor(after); ok && idx >= 0 && idx < n {
			startIdx = idx + 1
		}
	}
	if first <= 0 {
		first = n
	}
	endIdx := startIdx + first
	if endIdx > n {
		endIdx = n
	}
	for i := startIdx; i < endIdx; i++ {
		page = append(page, i)
	}
	hasNext = endIdx < n
	if len(page) > 0 {
		start = encodeCursor(page[0])
		end = encodeCursor(page[len(page)-1])
	}
	return page, hasNext, start, end
}

func encodeCursor(i int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("cursor:" + strconv.Itoa(i)))
}

func decodeCursor(s string) (int, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(s)
		if err != nil {
			return 0, false
		}
	}
	if !strings.HasPrefix(string(raw), "cursor:") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(string(raw), "cursor:"))
	if err != nil {
		return 0, false
	}
	return n, true
}

// isNotFound reports whether an error is the store's not-found error.
func isNotFound(err error) bool {
	return err != nil && (errors.Is(err, store.ErrNotFound) ||
		strings.Contains(err.Error(), "not found"))
}
