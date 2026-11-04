#!/usr/bin/env bash
# Writes the licence notices for the cffi_dist shared libraries: every Go
# module compiled into them, with its licence text. Needs Go and go-licenses.
# Usage: .github/nodetls/notices.sh <output file>   (run from the repo root)
set -euo pipefail

out="$1"
tmp="$(mktemp -d)"
saved="$tmp/licenses"
# cffi_dist itself is covered by this repository's LICENSE. fhttp is a fork of
# Go's net/http that ships without a licence file; its sources point to Go's.
(cd cffi_dist && go-licenses save . --save_path="$saved" \
  --ignore tls_client_cffi --ignore github.com/bogdanfinn/fhttp)

go_license="$(go env GOROOT)/LICENSE"
[ -f "$go_license" ] || { curl -fsSL "https://go.dev/LICENSE?m=text" -o "$tmp/go.LICENSE"; go_license="$tmp/go.LICENSE"; }

section() {
  printf '\n================================================================================\n%s\n================================================================================\n\n' "$1"
  cat "${@:2}"
}

{
  echo "Third-party notices for the tls-client shared libraries built from"
  echo "https://github.com/Sahil1337/tls-client (a fork of https://github.com/bogdanfinn/tls-client)."
  echo "Each component compiled into the libraries is listed with its licence."
  section "tls-client (this repository, including cffi_dist)" LICENSE
  section "The Go standard library and runtime, and github.com/bogdanfinn/fhttp (derived from Go's net/http)" "$go_license"
  (cd "$saved" && find . -type f | LC_ALL=C sort) | while read -r file; do
    section "${file#./}" "$saved/$file"
  done
} > "$out"
