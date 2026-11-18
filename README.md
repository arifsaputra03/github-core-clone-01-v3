[![make.fun · github-core-clone-01](https://make.fun/badge/github-core-clone-01.svg)](https://make.fun/bounty/github-core-clone-01?utm_source=github&utm_medium=readme&utm_campaign=github-core-clone-01)
[![make.fun · rank](https://make.fun/badge/github-core-clone-01/arifsaputra03/github-core-clone-01-v3/rank.svg)](https://make.fun/bounty/github-core-clone-01/leaderboard?utm_source=github&utm_medium=readme&utm_campaign=github-core-clone-01)
[![make.fun · reward](https://make.fun/badge/github-core-clone-01/arifsaputra03/github-core-clone-01-v3/reward.svg)](https://make.fun/bounty/github-core-clone-01?utm_source=github&utm_medium=readme&utm_campaign=github-core-clone-01)

# forge — a GitHub-compatible git forge (make.fun github-core-clone-01, v3)

One process, no UI, no outside service. A Go server speaks git smart HTTP
(protocol v2 upload-pack, v0 receive-pack), a minimal REST v3 API, and —
in Phase 2 — GraphQL and MCP. The default store is an embedded file-based
store; PostgreSQL is available for a self-hosted deployment.

## Layout

```
main.ts                  # entrypoint (Node 22 stdlib): spawn ./forge, register/poll /done
forge                    # the Go binary (static linux/amd64, <8 MiB)
go.mod
cmd/forge/               # entry: config, wiring, start server
internal/config/         # environment config (data dir, port, token, viewer)
internal/domain/         # entities: User, Repository, Issue, PullRequest,
                         # Label, Comment, Milestone
internal/store/          # Store interface
internal/store/embedded/ # file-based store (the default in the grading pod)
internal/store/postgres/ # PostgreSQL store (build tag: postgres)
internal/git/            # pure-Go git: objects, packs, refs, pkt-lines,
                         # upload-pack (v2 + v0), receive-pack (v0),
                         # contents API helpers, tree diff + three-way merge
internal/bare/           # bare-repository layout on disk
internal/service/        # use cases: repositories, issues, labels, comments,
                         # milestones, pull requests, contents, Relay ids
internal/http/           # router, auth, request-id, smart HTTP
internal/http/rest/      # GitHub REST v3: root/meta, users, repos, branches,
                         # commits, refs, contents, labels, issues, comments,
                         # pull requests, milestones
internal/graphql/        # GraphQL at POST /api/graphql: hand-rolled lexer,
                         # parser, executor; viewer/repository/issue/pull
                         # request, mutations, Relay ids, cursor pagination,
                         # introspection, batching
internal/mcp/            # MCP at POST /mcp: JSON-RPC 2.0 (server/discover,
                         # initialize, ping, tools/list, tools/call);
                         # 8 tools: create_repo, get_repo, create_issue,
                         # list_issues, get_issue, create_pull_request,
                         # merge_pull_request, list_pull_requests
migrations/001_init.sql  # PostgreSQL schema
scripts/build.sh         # build the binary (stripped, <8 MiB)
scripts/run-local.sh     # run the server locally (embedded store)
scripts/smoke.sh         # Phase 1 regression: real git CLI (clone/push/
                         # pull, non-fast-forward, 404, shallow+deepen,
                         # mixed push, delete ref, v0)
testdata/
```

## Local suite score (kit runner, 2026-11-18)

```
node --experimental-strip-types sdk/run.ts --url http://127.0.0.1:PORT \
  --token TOK --perf --json
# 120/120 checks passed — git 30/30, repos 15/15, issues 15/15,
# pulls 20/20, protocol 5/5, graphql 20/20, mcp 15/15
# feature 600 + agent 200 + perf 162 = 962/1000
# perf: push 33 (p50 1047.8 ms), clone 30 (p50 198.9 ms),
#       read 50 (p50 559.4 ms), write 49 (p50 93.4 ms)
```

The v1 entry scored 909 on the server; this build's local score is 962.
The local runner is the sealed suite's byte-identical copy, so a green run
here is evidence, not a guarantee: the final grade re-judges on fresh
seeded data in the trial pod.

## Run it locally

```sh
./scripts/run-local.sh
# forge: listening on 127.0.0.1:<port> ...
# FORGE_PORT=<port>
```

The server listens on 127.0.0.1 and an ephemeral port. The bearer token for
local testing is `FORGE_TOKEN` (default `dev-token` in run-local.sh). The
data directory is `FORGE_DATA_DIR` (default `./.data`); delete it to start
fresh.

## Test it (git CLI)

```sh
export TOKEN=dev-token
export BASE=http://127.0.0.1:<port>

# (a) Create a repository.
curl -s -X POST $BASE/api/v3/user/repos \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"gitconf-demo","description":"git conformance for adaiversen/gitconf-demo","private":true,"auto_init":false}'

# (b) Clone the empty repository.
git -c http.extraHeader="Authorization: Bearer $TOKEN" \
  clone $BASE/adaiversen/gitconf-demo.git demo

# (c) Commit and push.
cd demo
git config user.email adaiversen@example.invalid
git config user.name "ada iversen"
echo hello > README.md
git add README.md && git commit -m "initial"
git -c http.extraHeader="Authorization: Bearer $TOKEN" push origin main

# (d) Clone again elsewhere and compare.
git -c http.extraHeader="Authorization: Bearer $TOKEN" \
  clone $BASE/adaiversen/gitconf-demo.git demo2
diff -r demo demo2 --exclude=.git

# (e) Push again, then pull.
echo more >> README.md
git commit -am "second"
git -c http.extraHeader="Authorization: Bearer $TOKEN" push origin main
cd ../demo2 && git -c http.extraHeader="Authorization: Bearer $TOKEN" pull

# (f) A non-fast-forward push is rejected per ref.
git reset --hard HEAD~1
git -c http.extraHeader="Authorization: Bearer $TOKEN" push origin main
# -> "! [rejected] main -> main (non-fast-forward)"

# (g) A missing repository is a 404 and the server stays alive.
curl -s -o /dev/null -w '%{http_code}\n' \
  "$BASE/adaiversen/nope.git/info/refs?service=git-upload-pack" \
  -H "Authorization: Bearer $TOKEN"
curl -s $BASE/healthz
```

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `FORGE_DATA_DIR` | `./data` | Embedded store + bare repositories |
| `FORGE_PORT` | `0` (ephemeral) | Port to listen on |
| `FORGE_TOKEN` | — (dev mode) | Bearer token the server accepts |
| `MAKEFUN_SHOP_TOKEN` | — | Same as FORGE_TOKEN; what the trial runner sets |
| `FORGE_VIEWER_LOGIN` | `adaiversen` | Login the bearer token names |
| `DATABASE_URL` | — | Selects PostgreSQL (needs `-tags postgres`) |

The viewer login is the login the deterministic trial scenario names
(`adaiversen`). When the bearer token is a JWT carrying a `login` claim,
that claim is used instead.

## PostgreSQL (self-hosted only)

The grading pod has no PostgreSQL, so the postgres store is behind the
`postgres` build tag and is **not** linked into the default binary (the
binary must stay under 8 MiB).

```sh
go build -tags postgres -ldflags "-s -w" -o forge ./cmd/forge
DATABASE_URL=postgres://user:pass@localhost:5432/forge ./forge
```

The schema is `migrations/001_init.sql` (also embedded in the postgres
package, which runs it on open). **Integration test status: NOT RUN** — no
PostgreSQL server is available in this VM, so the postgres code compiles
(`go build -tags postgres ./...` passes) but has never run against a live
database. The embedded store is the fully tested one.

## Phase 1 scope

Done: Go scaffolding (Clean Architecture), embedded store (default),
PostgreSQL store + migration (compiles; integration test not run), git
smart HTTP (v2 upload-pack: ls-refs + fetch, shallow; v0 receive-pack:
report-status, side-band-64k, delete-refs, fast-forward enforcement),
bare-repository initialisation on create, REST minimal (POST
/api/v3/user/repos, GET /api/v3/repos/{owner}/{repo}), main.ts lifecycle.

Phase 2 (not started): full REST (issues, pulls, labels, ...),
GraphQL at /api/graphql, MCP at /mcp, and the performance work (push /
clone / read / write workloads).
