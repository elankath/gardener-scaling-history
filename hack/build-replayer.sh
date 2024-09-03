#!/usr/bin/env bash
set -eo pipefail

echoErr() { echo "$@" 1>&2; }

echo "howdy"
#if command -v "gum" > /dev/null 2>&1; then
#        echo "The command '$1' is available."
#        exit 0
#    else
#        echo "The command '$1' is not available."
#        return 1
#fi
#if  ! command -v "gum" ; then
#  echo "Installing Gum..."
#  brew install gum
#fi
mode="$1"
if [[ -z "$mode" ]]; then
  echoErr "$0 needs mode: ('local' or 'remote') to be specified! ie. specify $0 local or $0 remote"
  exit 1
fi

if [[ "$mode" != "local" && "$mode" != "remote" ]]; then
  echoErr "Unknown mode $mode. Only 'local' or 'remote supported presently"
  exit 1
fi

if [[ "$mode" == "local" ]]; then
  goos=$(go env GOOS)
  goarch=$(go env GOARCH)
else
  goos=linux
  goarch=amd64
fi
echo "GOOS set to $goos, GOARCH set to $goarch"
binDir="$(realpath bin)/$mode"
echo "Will build binaries into $binDir"

if [[ "$mode" == "remote" && -z "$DOCKERHUB_USER" ]]; then
  echoErr "Please export DOCKERHUB_USER var before executing this script and ensure you have logged in using 'docker login'"
  exit 1
fi

if [[ -z "$KVCL_DIR" ]]; then
  KVCL_DIR="$GOPATH/src/github.com/unmarshall/kvcl"
  echo "KVCL_DIR is not set. Assuming default: $KVCL_DIR"
  if [[ ! -d "$KVCL_DIR" ]]; then
    echoErr "Default dir assumption of KVCL_DIR: $KVCL_DIR doesn't exist. Kindly check out at this path or explicitly set KVCL_DIR before invoking this script"
    exit 2
  fi
fi

if [[ -z "$VCA_DIR" ]]; then
  VCA_DIR="$GOPATH/src/github.com/elankath/gardener-virtual-autoscaler"
  echo "VCA_DIR is not set. Assuming default: $VCA_DIR"
  if [[ ! -d "$VCA_DIR" ]]; then
    echoErr "Default dir assumption of VCA_DIR: $VCA_DIR doesn't exist. Kindly check out at this path or explicitly set VCA_DIR before invoking this script"
    exit 2
  fi
fi

echo "Building kvcl..."
pushd "$KVCL_DIR" > /dev/null
GOOS=$goos GOARCH=$goarch go build -o "$binDir/kvcl" cmd/main.go
chmod +x "$binDir/kvcl"

popd > /dev/null
echo "Building virtual cluster autoscaler..."
pushd "$VCA_DIR/cluster-autoscaler" > /dev/null
GOOS=$goos GOARCH=$goarch go build -o "$binDir/cluster-autoscaler" main.go
chmod +x "$binDir/cluster-autoscaler"
popd

echo "Build done. Please check binaries in $binDir"

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
