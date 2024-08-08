#!/usr/bin/env bash
set -eo pipefail

echo "Please make sure you have logged into your docker hub account using 'docker login' \
and set the TAG env for the image tag before running this script"
mkdir -p bin
if [[ -f bin/conntest ]]; then
  echo "Removing existing binary."
  rm bin/conntest
fi
echo "Building conntest for linux/amd64..."
GOOS=linux GOARCH=amd64 go build -v -o bin/conntest cmd/conntest/main.go
chmod +x bin/conntest
tag=$TAG
defaultTag="lenkite/conntest:latest"
if [[ -z "$tag" ]]; then
  echo "TAG env is missing. Assuming $defaultTag"
  tag="$defaultTag"
fi
docker buildx build --push --platform linux/amd64 --tag $tag -f hack/ConnTestDockerfile .