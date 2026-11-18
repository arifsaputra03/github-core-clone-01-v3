// Entry point for the github-core-clone-01 forge (Node 22 stdlib only).
//
// The forge itself is the Go binary ./forge, next to this file. This file is
// only the harness lifecycle the env expects:
//
//   1. MAKEFUN_ENV_URL names the env's origin. Without it there is nothing
//      to talk to, so the program exits 1 at once.
//   2. Spawn ./forge. It binds an ephemeral port (never 8080, which the env
//      owns) and prints FORGE_PORT=<port> on its stdout when it is listening.
//   3. POST $MAKEFUN_ENV_URL/register {"port": N}. 200 is the normal answer;
//      409 means another entry already registered first, and this process
//      keeps waiting like any other. 400 and 405 are contract errors and end
//      the program; anything else is retried, since the env may still be
//      starting.
//   4. GET $MAKEFUN_ENV_URL/done until the body is {"done": true}. The env
//      answers at once ({"done": false} while the suite runs), so this is a
//      poll. A failed or slow poll is retried with a short exponential
//      backoff; the process never exits before the env says done.
//   5. Stop the forge and exit 0. The harness then stops the env, which
//      writes the export the scorer reads.
//
// The forge inherits this process's environment, so MAKEFUN_ENV_URL and the
// token reach it unchanged.

import { spawn } from 'node:child_process';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const ENV_URL = (process.env.MAKEFUN_ENV_URL ?? '').replace(/\/+$/, '');
if (ENV_URL === '') {
  process.stderr.write('[entry] MAKEFUN_ENV_URL is not set: there is no env to register with\n');
  process.exit(1);
}

// One register or done request may take this long before it is abandoned.
const REQUEST_TIMEOUT_MS = 30_000;
// The first wait between retries, and the longest wait.
const RETRY_FIRST_MS = 250;
const RETRY_MAX_MS = 2_000;

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// The wait after `attempt` failed requests: exponential, capped.
function retryDelay(attempt) {
  return Math.min(RETRY_MAX_MS, RETRY_FIRST_MS * 2 ** Math.min(attempt, 8));
}

// POST the bound port until the env accepts it.
async function register(port) {
  for (let attempt = 0; ; attempt += 1) {
    try {
      const res = await fetch(`${ENV_URL}/register`, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ port }),
        signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
      });
      if (res.status === 200 || res.status === 409) return;
      if (res.status === 400 || res.status === 405) {
        throw new Error(`register answered HTTP ${res.status}: ${await res.text()}`);
      }
    } catch (err) {
      if (err instanceof Error && err.message.startsWith('register answered HTTP ')) throw err;
    }
    await sleep(retryDelay(attempt));
  }
}

// Poll GET /done until it answers {"done": true}; never return before that.
async function waitForDone() {
  for (let attempt = 0; ; attempt += 1) {
    try {
      const res = await fetch(`${ENV_URL}/done`, { signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS) });
      if (res.ok) {
        const body = await res.json();
        if (body && body.done === true) return;
      }
    } catch {
      // The env is not answering yet, or the poll timed out: not done.
    }
    await sleep(retryDelay(attempt));
  }
}

// Start ./forge and resolve with its port once it prints FORGE_PORT=<port>.
// The child's stderr is forwarded, so its log is visible in the entry's.
function startForge() {
  const here = dirname(fileURLToPath(import.meta.url));
  const binary = join(here, 'forge');
  // The trial pod mounts /bot (this repository) read-only, so the forge
  // cannot keep its data under ./data there: its store would fail to create
  // the directory and the forge would exit before listening. Give it a
  // fresh writable directory under the OS temp dir, unless the caller
  // already chose one (FORGE_DATA_DIR).
  const dataDir = process.env.FORGE_DATA_DIR ?? mkdtempSync(join(tmpdir(), 'forge-'));
  process.stdout.write(`[entry] forge data dir ${dataDir}\n`);
  const child = spawn(binary, [], {
    stdio: ['ignore', 'pipe', 'inherit'],
    env: { ...process.env, FORGE_DATA_DIR: dataDir },
  });
  return new Promise((resolve, reject) => {
    let stdout = '';
    const onData = (chunk) => {
      stdout += chunk.toString();
      process.stdout.write(chunk);
      const match = stdout.match(/FORGE_PORT=(\d+)/);
      if (match) {
        child.stdout.off('data', onData);
        resolve({ child, port: Number(match[1]) });
      }
    };
    child.stdout.on('data', onData);
    child.once('error', (err) => reject(new Error(`could not start ${binary}: ${err.message}`)));
    child.once('exit', (code) => {
      reject(new Error(`forge exited early with code ${code}`));
    });
  });
}

async function main() {
  const { child, port } = await startForge();
  process.stdout.write(`[entry] forge listening on port ${port}\n`);

  await register(port);
  process.stdout.write('[entry] registered; waiting for the env to say done\n');

  await waitForDone();

  child.kill('SIGTERM');
  await new Promise((resolve) => child.once('exit', resolve));
  process.stdout.write('[entry] done\n');
}

try {
  await main();
  process.exit(0);
} catch (err) {
  process.stderr.write(`[entry] ${err instanceof Error ? err.message : String(err)}\n`);
  process.exit(1);
}
