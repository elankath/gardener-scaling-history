#!/usr/bin/env bash
set -eo pipefail

echoErr() { echo "$@" 1>&2; }

mkdir -p "bin/remote"
mode="$1"
if [[ -z "$mode" ]]; then
  echoErr "$0 needs build mode: ('local' or 'remote') to be specified! ie. specify $0 local or $0 remote"
  exit 1
fi

if [[ ! -f cfg/clusters.csv ]]; then
  echoErr "Please ensure that you are in the base dir of the gardener-scaling-history repo before running this script"
  exit 2
fi

if [[ "$mode" != "local" && "$mode" != "remote" ]]; then
  echoErr "Unknown build mode $mode. Only 'local' or 'remote supported presently"
  exit 1
fi

if [[ -z "$GOPATH" ]]; then
  GOPATH=$(go env GOPATH)
  echo "GOPATH is not set. Assuming default: $GOPATH"
fi

if [[ "$mode" == "local" ]]; then
  goos=$(go env GOOS)
  goarch=$(go env GOARCH)
  binDir="$(realpath bin)"
else
  goos=linux
  goarch=amd64
  binDir="$(realpath bin)/$mode"
fi
echo "GOOS set to $goos, GOARCH set to $goarch"
echo "For build mode $mode, will build binaries into $binDir"

if [[ "$mode" == "remote" && -z "$DOCKERHUB_USER" ]]; then
  echoErr "Please export DOCKERHUB_USER var before executing this script and ensure you have logged in using 'docker login'"
  exit 1
fi

printf "Installing setup-envtest...\n"
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
envTestSetupCmd="setup-envtest --os $goos --arch $goarch use -p path"
printf "Executing: %s\n" "$envTestSetupCmd"
binaryAssetsDir=$(eval "$envTestSetupCmd")
errorCode="$?"
if [[ "$errorCode" -gt 0 ]]; then
      echoErr "EC: $errorCode. Error in executing $envTestSetupCmd. Exiting!"
      exit 1
fi
echo "setup-envtest downloaded binaries into $binaryAssetsDir"
cp -fv "$binaryAssetsDir"/* "$binDir"
echo "Copied binaries into $binDir"

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

if [[ -z "$SR_DIR" ]]; then
  SR_DIR="$GOPATH/src/github.com/unmarshall/scaling-recommender"
  echo "SR_DIR (scaling recommender) is not set. Assuming default: $SR_DIR"
  if [[ ! -d "$SR_DIR" ]]; then
    echoErr "Default dir assumption of SR_DIR: $SR_DIR doesn't exist. Kindly check out at this path or explicitly set SR_DIR before invoking this script"
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

popd > /dev/null
echo "Building scaling recommender..."
pushd "$SR_DIR" > /dev/null
GOOS=$goos GOARCH=$goarch go build -o "$binDir/scaling-recommender" main.go
chmod +x "$binDir/scaling-recommender"
popd

echo "Building replayer..."

GOOS=$goos GOARCH=$goarch go build -v -o "$binDir/replayer" cmd/replayer/main.go
echo "Build done. Please check binaries in $binDir"

if [[ "$mode" == "local" ]]; then
  exit 0
fi

echo "NOTE: Please ensure that Docker Desktop is started."
chmod +x "$binDir"/replayer
REPLAYER_IMAGE_TAG="$DOCKERHUB_USER/scaling-history-replayer:latest"
export REPLAYER_IMAGE_TAG

echo "Building and pushing to $REPLAYER_IMAGE_TAG..."
docker buildx build -f replayer/Dockerfile --push --platform linux/amd64 --tag "$REPLAYER_IMAGE_TAG" .
#docker buildx build -f replayer/Dockerfile --tag "$REPLAYER_IMAGE_TAG" .