#!/usr/bin/env bash
set -eo pipefail

echoerr() { echo "$@" 1>&2; }

binDir="$(realpath bin)"

if [[ -f "$GOPATH/src/github.com/unmarshall/kvcl/launch.env" ]]; then
  source "$GOPATH/src/github.com/unmarshall/kvcl/launch.env"
fi

if [[ -z "$BINARY_ASSETS_DIR" ]]; then
  echo "Please ensure that BINARY_ASSETS_DIR is set to location where setup-envtest of kvcl has downloaded binaries for this OS and Arch"
  exit 1
fi

## How to do this ?
## build-replayer should build binaries and setup-env test for both docker and local mode.
## replay-db should work in both container and local mode.









