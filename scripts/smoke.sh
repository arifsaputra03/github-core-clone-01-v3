#!/bin/sh
# Phase 1 regression smoke tests for the forge, run against the real git
# CLI (2.43.0): create/clone/push/pull, non-fast-forward rejection, 404s,
# shallow clone + deepen, mixed push, ref deletion, and the v0 protocol.
# Usage: sh scripts/smoke.sh   (run from the repository root)
set -u
cd "$(dirname "$0")/.."

GO="$HOME/workspace/go/go/bin/go"
if [ ! -x "$GO" ]; then GO=go; fi

DATA=$(mktemp -d /tmp/forge-smoke-XXXXXX)
WORK=$(mktemp -d /tmp/forge-work-XXXXXX)
trap 'kill $PID 2>/dev/null; rm -rf "$DATA" "$WORK"' EXIT INT TERM

FORGE_DATA_DIR="$DATA" FORGE_TOKEN=test-token FORGE_PORT=0 ./forge > "$DATA/server.log" 2>&1 &
PID=$!
for _ in $(seq 1 50); do
  grep -q '^FORGE_PORT=' "$DATA/server.log" 2>/dev/null && break
  sleep 0.2
done
PORT=$(sed -n 's/^FORGE_PORT=//p' "$DATA/server.log" | head -1)
if [ -z "$PORT" ]; then echo "FAIL: server did not print FORGE_PORT"; cat "$DATA/server.log"; exit 1; fi
BASE="http://127.0.0.1:$PORT"
# The bearer token for every git invocation, via git's config env
# (a -c flag would break on the space inside the header value).
export GIT_CONFIG_COUNT=1
export GIT_CONFIG_KEY_0=http.extraHeader
export GIT_CONFIG_VALUE_0="Authorization: Bearer test-token"
echo "server on $BASE (data $DATA)"

pass=0; fail=0
ok()  { echo "PASS: $1"; pass=$((pass+1)); }
bad() { echo "FAIL: $1"; fail=$((fail+1)); }

# 1. Create a repository (201).
CODE=$(curl -s -o "$DATA/repo.json" -w '%{http_code}' -X POST "$BASE/api/v3/user/repos" \
  -H 'Authorization: Bearer test-token' -H 'Content-Type: application/json' \
  -d '{"name":"smoke","private":false}')
[ "$CODE" = 201 ] && ok "create repo 201" || bad "create repo: HTTP $CODE"

# 2. Clone the empty repository.
git clone -q "$BASE/adaiversen/smoke.git" "$WORK/empty" 2>"$DATA/err" \
  && ok "clone empty repo" || { bad "clone empty repo"; cat "$DATA/err"; }

# 3. Commit + push, then clone again and compare trees.
cd "$WORK/empty" || exit 1
git config user.email smoke@example.invalid; git config user.name Smoke
printf 'hello\n' > a.txt; git add a.txt; git commit -qm 'first'
git push -q origin main 2>"$DATA/err" \
  && ok "push first commit" || { bad "push first commit"; cat "$DATA/err"; }
cd "$WORK" || exit 1
git clone -q "$BASE/adaiversen/smoke.git" "$WORK/clone2" 2>"$DATA/err" \
  && ok "clone again" || { bad "clone again"; cat "$DATA/err"; }
T1=$(git -C "$WORK/empty" rev-parse HEAD); T2=$(git -C "$WORK/clone2" rev-parse HEAD)
[ "$T1" = "$T2" ] && [ "$(cat "$WORK/clone2/a.txt")" = "hello" ] \
  && ok "clone2 identical to the pushed commit ($T1)" \
  || bad "clone2 differs: $T1 vs $T2"

# 4. Push a second commit, pull it into clone2, compare.
cd "$WORK/empty" || exit 1
printf 'second\n' > b.txt; git add b.txt; git commit -qm 'second'
git push -q origin main 2>"$DATA/err" \
  && ok "push second commit" || { bad "push second commit"; cat "$DATA/err"; }
cd "$WORK/clone2" || exit 1
git pull -q origin main 2>"$DATA/err" \
  && ok "pull second commit" || { bad "pull second commit"; cat "$DATA/err"; }
[ "$(git rev-parse HEAD)" = "$(git -C "$WORK/empty" rev-parse HEAD)" ] \
  && [ -f b.txt ] && ok "pull identical" || bad "pull differs"

# 5. Non-fast-forward push is rejected (exit 1) and the server ref does not move.
BEFORE=$(git -C "$WORK/empty" rev-parse HEAD)
cd "$WORK/clone2" || exit 1
git config user.email smoke@example.invalid; git config user.name Smoke
git reset -q --hard HEAD~1
printf 'diverged\n' > c.txt; git add c.txt; git commit -qm 'diverged'
git push -q origin main 2>"$DATA/err"
CODE=$?
if [ "$CODE" -eq 0 ]; then
  bad "non-fast-forward push was accepted"
else
  ok "non-fast-forward push rejected (exit $CODE)"
fi
AFTER=$(git -C "$WORK/empty" rev-parse HEAD)
[ "$BEFORE" = "$AFTER" ] && ok "server ref unmoved after the rejected push" \
  || bad "server ref moved: $BEFORE -> $AFTER"

# 6. A missing repository is a 404 and the server is still alive.
CODE=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/adaiversen/nope.git/info/refs?service=git-upload-pack" \
  -H 'Authorization: Bearer test-token')
[ "$CODE" = 404 ] && ok "missing repo info/refs 404" || bad "missing repo: HTTP $CODE"
CODE=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/healthz")
[ "$CODE" = 200 ] && ok "server alive after the 404" || bad "server not alive: HTTP $CODE"

# 7. Shallow clone, then deepen.
cd "$WORK" || exit 1
git clone -q --depth 1 "$BASE/adaiversen/smoke.git" "$WORK/shallow" 2>"$DATA/err" \
  && ok "shallow clone --depth 1" || { bad "shallow clone"; cat "$DATA/err"; }
[ "$(git -C "$WORK/shallow" rev-list --count HEAD)" = 1 ] \
  && ok "shallow clone has exactly 1 commit" || bad "shallow clone depth"
git -C "$WORK/shallow" fetch -q --deepen 1 origin main 2>"$DATA/err" \
  && [ "$(git -C "$WORK/shallow" rev-list --count HEAD)" = 2 ] \
  && ok "deepen 1 gives 2 commits" || { bad "deepen"; cat "$DATA/err"; }

# 8. Mixed push: a new branch plus a new commit on main, in one push.
cd "$WORK/empty" || exit 1
git checkout -qb side
printf 'side\n' > s.txt; git add s.txt; git commit -qm 'side work'
git checkout -q main
printf 'third\n' > d.txt; git add d.txt; git commit -qm 'third'
git push -q origin main side 2>"$DATA/err" \
  && ok "mixed push (main + side)" || { bad "mixed push"; cat "$DATA/err"; }
git ls-remote "$BASE/adaiversen/smoke.git" > "$DATA/refs" 2>"$DATA/err"
grep -q 'refs/heads/side' "$DATA/refs" \
  && ok "side branch visible after the mixed push" || bad "side branch missing"

# 9. Delete a ref.
git push -q origin :side 2>"$DATA/err" \
  && ok "delete ref side" || { bad "delete ref"; cat "$DATA/err"; }
git ls-remote "$BASE/adaiversen/smoke.git" > "$DATA/refs" 2>"$DATA/err"
grep -q 'refs/heads/side' "$DATA/refs" \
  && bad "side branch still listed after deletion" || ok "side branch gone after deletion"

# 10. The v0 protocol: clone with --no-local using protocol v0.
cd "$WORK" || exit 1
git -c protocol.version=0 clone -q "$BASE/adaiversen/smoke.git" "$WORK/v0" 2>"$DATA/err" \
  && ok "v0 clone" || { bad "v0 clone"; cat "$DATA/err"; }
[ "$(git -C "$WORK/v0" rev-parse HEAD)" = "$(git -C "$WORK/empty" rev-parse HEAD)" ] \
  && ok "v0 clone identical" || bad "v0 clone differs"

echo "---"
echo "smoke: $pass passed, $fail failed"
[ "$fail" = 0 ]
