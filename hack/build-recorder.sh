#!/usr/bin/env zsh
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

if whence -w  x86_64-unknown-linux-gnu-gcc > /dev/null 2>&1; then
    echo "x86_64-unknown-linux-gnu-gcc exists!  Will use for CGO Cross Compilation."
else
    echo "x86_64-unknown-linux-gnu-gcc does not exist. Installing via brew..."
    brew tap SergioBenitez/osxct
    brew install SergioBenitez/osxct/x86_64-unknown-linux-gnu
    echo "Installed x86_64-unknown-linux-gnu-gcc -> QUIT TERMINAL, OPEN FRESH TERMINAL AND RUN THIS SCRIPT: '$0' AGAIN"
    exit 3
fi

echo "Please ensure that Docker Desktop is started."
mkdir -p bin
if [[ -f bin/remote/recorder ]]; then
  echo "Removing existing remote binary."
  rm bin/remote/recorder
fi
echo "Building recorder for linux/amd64..."
CC=x86_64-unknown-linux-gnu-gcc CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -v -o bin/remote/recorder cmd/recorder/main.go
chmod +x bin/remote/recorder
RECORDER_IMAGE_TAG="$DOCKERHUB_USER/scaling-history-recorder:latest"
export RECORDER_IMAGE_TAG

echo "Docker building and pushing to $APP_IMAGE_TAG..."
dockerBin=$(which docker)
echo "Using this docker: $dockerBin"
echo "Building and pushing to $RECORDER_IMAGE_TAG..."
docker buildx build -f recorder/Dockerfile --push --platform linux/amd64 --tag "$RECORDER_IMAGE_TAG" .
