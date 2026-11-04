# node-tls-client builds of tls-client

This fork publishes the shared libraries [node-tls-client](https://github.com/Sahil1337/node-tls-client) downloads with `initTLS({ binary: "node-tls-client" })`.

**What differs from upstream:** two new files. [`cffi_dist/async.go`](../../cffi_dist/async.go) adds `requestAsync` (start a request on a goroutine, get a ticket back at once) and `waitNext` (collect the next finished request), so a host can keep any number of requests in flight from one waiting thread instead of one blocked thread per request. [`cffi_dist/notify.go`](../../cffi_dist/notify.go) adds `setNotify` (a function the library calls from its own thread whenever requests have finished; the host then collects them with `waitNext(0)`), so a host that can take such a call, like node-tls-client, needs no waiting thread at all. Everything else, including the `request` export, is upstream's code at the tagged version.

**Releases:** tag `v<tls-client version>-nodetls.<n>`, e.g. `v1.18.0-nodetls.1`. [`nodetls-release.yml`](../workflows/nodetls-release.yml) builds every platform with xgo (upstream's asset names), smoke-tests Linux x64/arm64, macOS arm64 and Windows x64 with [`smoke.cjs`](smoke.cjs), and publishes the libraries with `THIRD_PARTY_NOTICES.txt` ([`notices.sh`](notices.sh)) and `SHA256SUMS`. Verify a download with `sha256sum -c SHA256SUMS --ignore-missing`.

**Keeping up with upstream:** [`nodetls-sync.yml`](../workflows/nodetls-sync.yml) runs daily. When upstream has tagged a newer version, it merges that tag into `master`, points `cffi_dist/go.mod` at it, tags `v<version>-nodetls.1` and releases. If it fails (merge conflict, failed build, or an upstream change to `.github/workflows`), do it by hand:

```sh
git fetch upstream --tags
git merge v1.19.0                 # never rebase master
cd cffi_dist && go get github.com/bogdanfinn/tls-client@v1.19.0 && go mod tidy && cd ..
git commit -am "cffi_dist: build against tls-client v1.19.0"
git tag v1.19.0-nodetls.1
git push origin master v1.19.0-nodetls.1   # the tag push runs the release
```

A fix of our own on the same upstream version is `-nodetls.2`, and so on.

**Setup (once):** enable Actions on the fork (Actions tab). GitHub turns off scheduled workflows after 60 days without commits; re-enable `nodetls sync` there if that happens.
