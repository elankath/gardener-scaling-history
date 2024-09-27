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

if whence -w x86_64-unknown-linux-gnu-gcc > /dev/null 2>&1; then
    echo "x86_64-unknown-linux-gnu-gcc exists!  Will use for CGO Cross Compilation."
else
    echo "x86_64-unknown-linux-gnu-gcc does not exist. Installing via brew..."
    brew tap SergioBenitez/osxct
    brew install SergioBenitez/osxct/x86_64-unknown-linux-gnu
    echo "Installed x86_64-unknown-linux-gnu-gcc -> OPEN FRESH TERMINAL AND RUN THIS SCRIPT: '$0' AGAIN"
    exit 3
fi
echo "Building app for linux/amd64..."
CC=x86_64-unknown-linux-gnu-gcc CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -v -o "$appBinPath" cmd/app/main.go
chmod +x "$appBinPath"
APP_IMAGE_TAG="$DOCKERHUB_USER/scaling-history-app:latest"
export APP_IMAGE_TAG

dockerBin=$(which docker)
echo "Using this docker: $dockerBin"
echo "Docker building and pushing to $APP_IMAGE_TAG..."
docker buildx build -f app/Dockerfile --push --platform linux/amd64 --tag "$APP_IMAGE_TAG" .
