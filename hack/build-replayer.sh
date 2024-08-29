#!/usr/bin/env bash
set -eo pipefail

echoErr() { echo "$@" 1>&2; }

if [[ -z "$DOCKERHUB_USER" ]]; then
  echoErr "Please export DOCKERHUB_USER var before executing this script and ensure you have logged in using 'docker login'"
  exit 1
fi

if [[ -z "$KVCL_DIR" ]]; then
  KVCL_DIR="$GOPATH/src/github.com/unmarshall/kvcl"
  echo "KVCL_DIR is not set. Assuming default: $KVCL_DIR"
fi

if [[ ! -d "$KVCL_DIR" ]]; then
  echoErr "Please ensure kvcl is checked out at $KVCL_DIR"
  exit 3
fi

if [[ -z "$VCA_DIR" ]]; then
  VCA_DIR="$GOPATH/src/github.com/elankath/gardener-virtual-autoscaler"
  echo "VCA_DIR is not set. Assuming default: $VCA_DIR"
fi

if [[ ! -d "$VCA_DIR" ]]; then
  echoErr "Please ensure virtual cluster autoscaler repository is checked out at $VCA_DIR"
  exit 4
fi

binDir="$(realpath bin)"

echo "Building kvcl..."
pushd "$KVCL_DIR"
GOOS=linux GOARCH=amd64 go build -o "$binDir/kvcl" cmd/main.go
chmod +x bin/kvcl

popd

echo "Building virtual cluster autoscaler..."
pushd "$VCA_DIR/cluster-autoscaler"
GOOS=linux GOARCH=amd64 go build -o "$binDir/cluster-autoscaler" main.go
chmod +x bin/cluster-autoscaler

#echo "Please ensure that Docker Desktop is started."
#mkdir -p bin
#if [[ -f bin/recorder ]]; then
#  echo "Removing existing binary."
#  rm bin/recorder
#fi
#echo "Building recorder for linux/amd64..."
#GOOS=linux GOARCH=amd64 go build -v -o bin/recorder cmd/recorder/main.go
#chmod +x bin/recorder
#GSH_IMAGE_TAG="$DOCKERHUB_USER/scaling-history-recorder:latest"
#export GSH_IMAGE_TAG
#
#echo "Building and pushing to $GSH_IMAGE_TAG..."
#docker buildx build --push --platform linux/amd64 --tag "$GSH_IMAGE_TAG" .
