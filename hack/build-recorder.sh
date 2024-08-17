#!/usr/bin/env bash
set -eo pipefail

echoErr() { echo "$@" 1>&2; }

if [[ -z "$DOCKERHUB_USER" ]]; then
  echoErr "Please export DOCKERHUB_USER var before executing this script and ensure you have logged in using 'docker login'"
  exit 1
fi
if [[ ! -f cfg/clusters.csv ]]; then
  echoErr "Please ensure that you are in the base dir of the gardener-scaling-history repo before running this script"
  exit 2
fi

echo "Please ensure that Docker Desktop is started."
mkdir -p bin
if [[ -f bin/recorder ]]; then
  echo "Removing existing binary."
  rm bin/recorder
fi
echo "Building recorder for linux/amd64..."
GOOS=linux GOARCH=amd64 go build -v -o bin/recorder cmd/recorder/main.go
chmod +x bin/recorder
GSH_IMAGE_TAG="$DOCKERHUB_USER/scaling-history-recorder:latest"
export GSH_IMAGE_TAG

echo "Building and pushing to $GSH_IMAGE_TAG..."
docker buildx build --push --platform linux/amd64 --tag "$GSH_IMAGE_TAG" .
