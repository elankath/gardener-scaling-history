#!/usr/bin/env bash
set -eo pipefail

echoErr() { echo "$@" 1>&2; }

if [[ -z "$DOCKERHUB_USER" ]]; then
  echoErr "Please export DOCKERHUB_USER var before executing this script and ensure you have logged in using 'docker login'"
  exit 1
fi

appBinPath="bin/remote/app"
echo "Please ensure that Docker Desktop is started."
mkdir -p bin/remote
if [[ -f "$appBinPath" ]]; then
  echo "Removing existing binary."
  rm "$appBinPath"
fi
echo "Building app for linux/amd64..."
CC=x86_64-unknown-linux-gnu-gcc CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -v -o "$appBinPath" cmd/app/main.go
chmod +x "$appBinPath"
APP_IMAGE_TAG="$DOCKERHUB_USER/scaling-history-app:latest"
export APP_IMAGE_TAG

echo "Building and pushing to $APP_IMAGE_TAG..."
docker buildx build -f app/Dockerfile --push --platform linux/amd64 --tag "$APP_IMAGE_TAG" .
