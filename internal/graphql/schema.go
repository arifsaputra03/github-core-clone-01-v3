// Package graphql is the forge's GraphQL API at POST /api/graphql (see
// parse.go and exec.go).
package graphql

// typeDef is one type of the schema: its kind and, for object types, its
// fields in a fixed order (the order introspection reports them in).
type typeDef struct {
	kind     string // OBJECT, ENUM, INPUT_OBJECT, SCALAR
	fields   []string
	enumVals []string
}

// schema is the forge's GraphQL schema. It mirrors the GitHub v4 surface
// the suite exercises: viewer, repository, issue, pull request, the
// mutations, Relay global ids and introspection.
var schema = map[string]*typeDef{
	"Query":                     {kind: "OBJECT", fields: []string{"viewer", "repository", "node"}},
	"Mutation":                  {kind: "OBJECT", fields: []string{"createIssue", "closeIssue", "addComment", "createPullRequest", "mergePullRequest"}},
	"User":                      {kind: "OBJECT", fields: []string{"id", "login", "name", "email", "avatarUrl"}},
	"Repository":                {kind: "OBJECT", fields: []string{"id", "databaseId", "name", "nameWithOwner", "owner", "defaultBranchRef", "viewerPermission", "hasIssuesEnabled", "issue", "issues", "pullRequest", "pullRequests"}},
	"Ref":                       {kind: "OBJECT", fields: []string{"name", "target"}},
	"GitObject":                 {kind: "OBJECT", fields: []string{"oid"}},
	"Issue":                     {kind: "OBJECT", fields: []string{"id", "number", "title", "body", "state", "createdAt", "updatedAt", "author", "url"}},
	"IssueConnection":           {kind: "OBJECT", fields: []string{"nodes", "edges", "pageInfo", "totalCount"}},
	"IssueEdge":                 {kind: "OBJECT", fields: []string{"node", "cursor"}},
	"IssueComment":              {kind: "OBJECT", fields: []string{"id", "body", "author", "createdAt", "updatedAt"}},
	"IssueCommentEdge":          {kind: "OBJECT", fields: []string{"node", "cursor"}},
	"PageInfo":                  {kind: "OBJECT", fields: []string{"hasNextPage", "hasPreviousPage", "startCursor", "endCursor"}},
	"PullRequest":               {kind: "OBJECT", fields: []string{"id", "number", "title", "body", "state", "merged", "mergeable", "headRefName", "baseRefName", "headRef", "baseRef", "files", "author", "createdAt", "updatedAt"}},
	"PullRequestConnection":     {kind: "OBJECT", fields: []string{"nodes", "edges", "pageInfo", "totalCount"}},
	"PullRequestEdge":           {kind: "OBJECT", fields: []string{"node", "cursor"}},
	"PullRequestFileConnection": {kind: "OBJECT", fields: []string{"nodes", "edges", "pageInfo", "totalCount"}},
	"PullRequestFileEdge":       {kind: "OBJECT", fields: []string{"node", "cursor"}},
	"PullRequestFile":           {kind: "OBJECT", fields: []string{"path", "additions", "deletions", "changes"}},
	"CreateIssuePayload":        {kind: "OBJECT", fields: []string{"issue"}},
	"CloseIssuePayload":         {kind: "OBJECT", fields: []string{"issue"}},
	"AddCommentPayload":         {kind: "OBJECT", fields: []string{"commentEdge"}},
	"CreatePullRequestPayload":  {kind: "OBJECT", fields: []string{"pullRequest"}},
	"MergePullRequestPayload":   {kind: "OBJECT", fields: []string{"pullRequest"}},
	"Node":                      {kind: "INTERFACE", fields: []string{"id"}},
	"IssueState":                {kind: "ENUM", enumVals: []string{"OPEN", "CLOSED"}},
	"PullRequestState":          {kind: "ENUM", enumVals: []string{"OPEN", "CLOSED", "MERGED"}},
	"MergeableState":            {kind: "ENUM", enumVals: []string{"MERGEABLE", "CONFLICTING", "UNKNOWN"}},
	"RepositoryPermission":      {kind: "ENUM", enumVals: []string{"ADMIN", "MAINTAIN", "WRITE", "TRIAGE", "READ"}},
	"SearchType":                {kind: "ENUM", enumVals: []string{"ISSUE", "REPOSITORY", "USER"}},
	"OrderDirection":            {kind: "ENUM", enumVals: []string{"ASC", "DESC"}},
	"IssueOrderField":           {kind: "ENUM", enumVals: []string{"CREATED_AT", "UPDATED_AT", "COMMENTS"}},
	"__Type":                    {kind: "OBJECT", fields: []string{"name", "kind", "description", "fields", "enumValues", "inputFields"}},
	"__Field":                   {kind: "OBJECT", fields: []string{"name", "description", "type", "args"}},
	"__EnumValue":               {kind: "OBJECT", fields: []string{"name", "description"}},
	"__InputValue":              {kind: "OBJECT", fields: []string{"name", "description", "type"}},
	"__Schema":                  {kind: "OBJECT", fields: []string{"types", "queryType", "mutationType"}},
	"__TypeKind":                {kind: "ENUM", enumVals: []string{"SCALAR", "OBJECT", "INTERFACE", "UNION", "ENUM", "INPUT_OBJECT", "LIST", "NON_NULL"}},
	"String":                    {kind: "SCALAR"},
	"Int":                       {kind: "SCALAR"},
	"ID":                        {kind: "SCALAR"},
	"Boolean":                   {kind: "SCALAR"},
	"DateTime":                  {kind: "SCALAR"},
}

// fieldType returns the named return type of a field on a type, or "" when
// the field does not exist there. Meta-fields (__type, __schema,
// __typename) exist on every type.
func fieldType(typeName, field string) string {
	switch field {
	case "__typename":
		return "String"
	case "__type":
		return "__Type"
	case "__schema":
		return "__Schema"
	}
	switch typeName {
	case "Query":
		switch field {
		case "viewer":
			return "User"
		case "repository":
			return "Repository"
		case "node":
			return "Node"
		}
	case "Mutation":
		switch field {
		case "createIssue":
			return "CreateIssuePayload"
		case "closeIssue":
			return "CloseIssuePayload"
		case "addComment":
			return "AddCommentPayload"
		case "createPullRequest":
			return "CreatePullRequestPayload"
		case "mergePullRequest":
			return "MergePullRequestPayload"
		}
	case "User":
		switch field {
		case "id":
			return "ID"
		case "login", "name", "email", "avatarUrl":
			return "String"
		}
	case "Repository":
		switch field {
		case "id":
			return "ID"
		case "databaseId":
			return "Int"
		case "name", "nameWithOwner":
			return "String"
		case "owner":
			return "User"
		case "defaultBranchRef":
			return "Ref"
		case "viewerPermission":
			return "RepositoryPermission"
		case "hasIssuesEnabled":
			return "Boolean"
		case "issue":
			return "Issue"
		case "issues":
			return "IssueConnection"
		case "pullRequest":
			return "PullRequest"
		case "pullRequests":
			return "PullRequestConnection"
		}
	case "Ref":
		switch field {
		case "name":
			return "String"
		case "target":
			return "GitObject"
		}
	case "GitObject":
		if field == "oid" {
			return "String"
		}
	case "Issue":
		switch field {
		case "id":
			return "ID"
		case "number":
			return "Int"
		case "title", "body", "url":
			return "String"
		case "state":
			return "IssueState"
		case "createdAt", "updatedAt":
			return "DateTime"
		case "author":
			return "User"
		}
	case "IssueConnection":
		switch field {
		case "nodes":
			return "Issue"
		case "edges":
			return "IssueEdge"
		case "pageInfo":
			return "PageInfo"
		case "totalCount":
			return "Int"
		}
	case "IssueEdge":
		switch field {
		case "node":
			return "Issue"
		case "cursor":
			return "String"
		}
	case "IssueComment":
		switch field {
		case "id":
			return "ID"
		case "body":
			return "String"
		case "author":
			return "User"
		case "createdAt", "updatedAt":
			return "DateTime"
		}
	case "IssueCommentEdge":
		switch field {
		case "node":
			return "IssueComment"
		case "cursor":
			return "String"
		}
	case "PageInfo":
		switch field {
		case "hasNextPage", "hasPreviousPage":
			return "Boolean"
		case "startCursor", "endCursor":
			return "String"
		}
	case "PullRequest":
		switch field {
		case "id":
			return "ID"
		case "number":
			return "Int"
		case "title", "body":
			return "String"
		case "state":
			return "PullRequestState"
		case "merged":
			return "Boolean"
		case "mergeable":
			return "MergeableState"
		case "headRefName", "baseRefName":
			return "String"
		case "headRef", "baseRef":
			return "Ref"
		case "files":
			return "PullRequestFileConnection"
		case "author":
			return "User"
		case "createdAt", "updatedAt":
			return "DateTime"
		}
	case "PullRequestConnection":
		switch field {
		case "nodes":
			return "PullRequest"
		case "edges":
			return "PullRequestEdge"
		case "pageInfo":
			return "PageInfo"
		case "totalCount":
			return "Int"
		}
	case "PullRequestEdge":
		switch field {
		case "node":
			return "PullRequest"
		case "cursor":
			return "String"
		}
	case "PullRequestFileConnection":
		switch field {
		case "nodes":
			return "PullRequestFile"
		case "edges":
			return "PullRequestFileEdge"
		case "pageInfo":
			return "PageInfo"
		case "totalCount":
			return "Int"
		}
	case "PullRequestFileEdge":
		switch field {
		case "node":
			return "PullRequestFile"
		case "cursor":
			return "String"
		}
	case "PullRequestFile":
		switch field {
		case "path":
			return "String"
		case "additions", "deletions", "changes":
			return "Int"
		}
	case "CreateIssuePayload", "CloseIssuePayload":
		if field == "issue" {
			return "Issue"
		}
	case "AddCommentPayload":
		if field == "commentEdge" {
			return "IssueCommentEdge"
		}
	case "CreatePullRequestPayload", "MergePullRequestPayload":
		if field == "pullRequest" {
			return "PullRequest"
		}
	case "Node":
		if field == "id" {
			return "ID"
		}
	case "__Type":
		switch field {
		case "name", "kind", "description":
			return "String"
		case "fields":
			return "__Field"
		case "enumValues":
			return "__EnumValue"
		case "inputFields":
			return "__InputValue"
		}
	case "__Field":
		switch field {
		case "name", "description":
			return "String"
		case "type":
			return "__Type"
		case "args":
			return "__InputValue"
		}
	case "__EnumValue":
		if field == "name" || field == "description" {
			return "String"
		}
	case "__InputValue":
		switch field {
		case "name", "description":
			return "String"
		case "type":
			return "__Type"
		}
	case "__Schema":
		switch field {
		case "types":
			return "__Type"
		case "queryType", "mutationType":
			return "__Type"
		}
	}
	return ""
}

// hasField reports whether a type exposes a field.
func hasField(typeName, field string) bool {
	return fieldType(typeName, field) != ""
}
