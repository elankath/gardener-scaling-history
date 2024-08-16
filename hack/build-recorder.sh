#!/usr/bin/env bash
set -eo pipefail

echoErr() { echo "$@" 1>&2; }

echo "Please make sure you have logged into your docker hub account using 'docker login' \
and set the TAG env for the image tag before running this script"
mkdir -p bin
if [[ -f bin/recorder ]]; then
  echo "Removing existing binary."
  rm bin/recorder
fi
echo "Building recorder for linux/amd64..."
GOOS=linux GOARCH=amd64 go build -v -o bin/recorder cmd/recorder/main.go
chmod +x bin/recorder
tag=$TAG
defaultTag="aaronfernandes/recorder:latest"
if [[ -z "$tag" ]]; then
  echo "TAG env is missing. Assuming $defaultTag"
  tag="$defaultTag"
fi
docker buildx build --push --platform linux/amd64 --tag $tag .

#if [[ ! -f "/tmp/tls.key" ]]; then
#  echoErr "Please follow step 2 of https://github.wdf.sap.corp/kubernetes/landscape-setup#signing-oci-images-manually and save as /tmp/tls.key"
#  exit 1
#fi

#echo "Creating random password"
## cosign_password=$(cat /dev/urandom | base64 | tr -dc '0-9a-zA-Z' | head -c${1:-64})
#cosign_password=$(tr -cd '_A-Z-a-z-0-9!@#$%^&*()=+[]{}";:/?.>,<' < /dev/urandom | head -c${1:-64})
#
#echo "Creating cosign password"
#COSIGN_PASSWORD=${cosign_password} cosign -d import-key-pair --key /tmp/tls.key
#
#echo "signing image"
#COSIGN_PASSWORD=${cosign_password} cosign sign -d --key import-cosign.key $TAG

