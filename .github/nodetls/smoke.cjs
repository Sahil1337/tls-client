// Smoke test for one built shared library, as node-tls-client loads it (koffi):
// the blocking request() export, then the completion queue (requestAsync /
// waitNext) with many requests in flight, collected by waiting and by
// setNotify callbacks. Usage: node smoke.cjs <library>
const assert = require("node:assert/strict");
const http = require("node:http");
const path = require("node:path");
const koffi = require("koffi");

// Absolute, with the platform's separators: Windows' LoadLibrary wants backslashes.
const lib = koffi.load(path.resolve(process.argv[2]));
const request = lib.func("request", "string", ["string"]);
const freeMemory = lib.func("freeMemory", "void", ["string"]);
const requestAsync = lib.func("uint64_t requestAsync(const char *requestParams)");
const waitNext = lib.func("const char *waitNext(int timeoutMs, _Out_ uint64_t *ticket)");

const payload = (url) =>
  JSON.stringify({
    sessionId: "smoke",
    requestUrl: url,
    requestMethod: "GET",
    timeoutSeconds: 30,
    followRedirects: false,
    headers: { accept: "*/*" },
    headerOrder: ["accept"],
  });

/** Parses a response, frees native's copy, returns it. */
function take(raw) {
  const res = JSON.parse(raw);
  freeMemory(res.id);
  return res;
}

function waitNextAsync(timeoutMs) {
  const ticket = [0];
  return new Promise((resolve, reject) =>
    waitNext.async(timeoutMs, ticket, (err, raw) =>
      err ? reject(err) : resolve(raw === null ? null : { ticket: Number(ticket[0]), raw }),
    ),
  );
}

async function main() {
  const server = http.createServer((req, res) =>
    setTimeout(() => res.end(req.url), 20),
  );
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const base = `http://127.0.0.1:${server.address().port}`;

  // Nothing in flight: a poll and a short wait both come back empty.
  const ticket = [0];
  assert.equal(waitNext(0, ticket), null, "poll when idle");
  const t0 = Date.now();
  assert.equal(waitNext(50, ticket), null, "wait when idle");
  assert.ok(Date.now() - t0 >= 40, "the wait waited");

  // The blocking export is unchanged (on a threadpool thread, as node-tls-client
  // calls it: a blocking call here would also block this process's server).
  const sync = take(
    await new Promise((resolve, reject) =>
      request.async(payload(`${base}/sync`), (err, raw) => (err ? reject(err) : resolve(raw))),
    ),
  );
  assert.equal(sync.status, 200, sync.body);
  assert.equal(sync.body, "/sync");

  // Many requests through the queue: distinct tickets, each with its own response.
  const N = 500;
  const urls = new Map();
  for (let i = 0; i < N; i++) {
    const t = Number(requestAsync(payload(`${base}/q/${i}`)));
    assert.ok(t > 0 && !urls.has(t), `fresh ticket ${t}`);
    urls.set(t, `/q/${i}`);
  }
  // A request that fails comes back through the queue too, as status 0.
  const failing = Number(requestAsync(payload("http://127.0.0.1:1/refused")));

  const started = Date.now();
  let failed = null;
  while (urls.size > 0 || failed === null) {
    assert.ok(Date.now() - started < 30_000, "all requests finish");
    let next = await waitNextAsync(1000);
    while (next) {
      const res = take(next.raw);
      if (next.ticket === failing) failed = res;
      else {
        assert.equal(res.status, 200, res.body);
        assert.equal(res.body, urls.get(next.ticket));
        urls.delete(next.ticket);
      }
      const raw = waitNext(0, ticket);
      next = raw === null ? null : { ticket: Number(ticket[0]), raw };
    }
  }
  assert.equal(failed.status, 0, "connection refused is an error response");
  const queuedMs = Date.now() - started;

  // setNotify: native calls back from a Go thread (koffi runs the callback on
  // this one) whenever requests have finished, and each call collects them
  // with waitNext(0). No thread waits.
  const NotifyFn = koffi.pointer(koffi.proto("void", []));
  const setNotify = lib.func("setNotify", "void", [NotifyFn]);
  const pending = new Map();
  let calls = 0;
  const notifyStarted = Date.now();
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`setNotify: ${pending.size} requests never came back`)), 30_000);
    setNotify(
      koffi.register(() => {
        try {
          calls++;
          for (let raw = waitNext(0, ticket); raw !== null; raw = waitNext(0, ticket)) {
            const t = Number(ticket[0]);
            assert.equal(take(raw).body, pending.get(t));
            pending.delete(t);
          }
          if (pending.size === 0) {
            clearTimeout(timer);
            resolve();
          }
        } catch (e) {
          clearTimeout(timer);
          reject(e);
        }
      }, NotifyFn),
    );
    // Callbacks run on this thread, so none can come before the loop ends.
    for (let i = 0; i < N; i++) pending.set(Number(requestAsync(payload(`${base}/n/${i}`))), `/n/${i}`);
  });
  setNotify(null);
  server.close();
  console.log(
    `ok: ${N} queued requests in ${queuedMs} ms, ${N} notified in ${Date.now() - notifyStarted} ms (${calls} callbacks), ${process.platform}/${process.arch}`,
  );
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
