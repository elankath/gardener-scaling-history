#!/usr/bin/env zsh
set -eo pipefail

echoErr() { echo "$@" 1>&2; }

# Function to clean up background process
cleanup() {
    echo "Cleaning up..."
    if [[ -n "$pid" ]]; then
        kill "$pid" 2>/dev/null
        wait "$pid" 2>/dev/null
        echo "Background process with PID $pid terminated."
    fi
}

if [[ -z "$DOCKERHUB_USER" ]]; then
  echoErr "Please export DOCKERHUB_USER var before executing this script and ensure you have logged in using 'docker login'"
  exit 1
fi

if  ! command -v gum ; then
  echoErr "gum not installed. Kindly first install gum using 'brew install gum' or relevant command for your OS"
  exit 1
fi

if [[ ! -f specs/replayer.yaml ]]; then
  echoErr "Please ensure that you are in the base dir of the gardener-scaling-history repo before running this script"
  exit 2
fi

if [[ -z "$INPUT_DATA_PATH" ]]; then
  echoErr "Kindly download the recorded db using ./hack/download-db.sh and set the path to it in the INPUT_DATA_PATH env var"
  exit 3
fi

if [[ ! -f "$INPUT_DATA_PATH" ]]; then
  echoErr "DB does not exist at $INPUT_DATA_PATH. Kindly download the recorded db using ./hack/download-db.sh and set the path to it in the INPUT_DATA_PATH env var"
  exit 4
fi

if [[ "$INPUT_DATA_PATH" != *".db" ]]; then
  echoErr "file at $INPUT_DATA_PATH does not appear to be a db file. Kindly download the recorded db using ./hack/download-db.sh and set the path to it in the INPUT_DATA_PATH env var"
  exit 5
fi

echo "NOTE: Please ensure you have Gardener Live Landscape Access"
echo "NOTE: Please ensure that scaling-history-recorder Pod is running via ./hack/deploy-recorder.sh"
gardenctl target --garden sap-landscape-live --project garden-ops --shoot utility-int

if [[ -z "$INPUT_DATA_PATH" ]]; then
  # Set up trap to call cleanup function on script exit or interrupt
  trap cleanup EXIT
  echo "INPUT_DATA_PATH NOT specified. Getting list of recorded DB's from scaling-history-recorder.."
  echo "Executing  kubectl port-forward -n mcm-ca-team pod/scaling-history-recorder 8080:8080..."
  kubectl port-forward -n mcm-ca-team pod/scaling-history-recorder 8080:8080 &
  pid=$!
  sleep 6
  echo "Started port-forwarding with PID: $pid"
  echo "Downloading db list..."
  dbList=$(curl localhost:8080/api/db)
  printf ">> Found recorded databases \n: %s" $dbList
  echo
  echo "Kindly Select a DB for which to run the replayer to produce scenario report:"
  dbList=$(echo "$dbList" | tr '\n' ' ')
  chosenDb=$(gum choose $dbList)
  echo "You have chosen $chosenDb ! Will run replayer against this DB."
#  export INPUT_DATA_PATH="/db/$chosenDb"
  export DB_NAME=${chosenDb:t}
else
  export DB_NAME=${INPUT_DATA_PATH:t}
fi

export SCALER="ca"
export POD_SUFFIX=$(print -P "%{$(echo $RANDOM | md5sum | head -c 3)%}")
export POD_NAME="scaling-history-replayer-${SCALER}-${POD_SUFFIX}"
export POD_DATA_PATH="/db/${DB_NAME}"
echo "POD_DATA_PATH has been set to ${POD_DATA_PATH} for ${POD_NAME} pod."

export NONCE="$(date)"
export MEMORY="8Gi"
replayerPodYaml="/tmp/scaling-history-replayer.yaml"
envsubst < specs/replayer.yaml > "$replayerPodYaml"
echo "Substituted env variables in specs/replayer.yaml and wrote to $replayerPodYaml"
#kubectl delete job -n mcm-ca-team scaling-history-replayer || echo "scaling-history-replayer JOB not yet deployed."
sleep 1

#set +e
#caReplayPVC=$(kubectl -n mcm-ca-team get pvc scaling-history-reports-ca-1)
#if [[  -z "$caReplayPVC" ]]; then
#  echo "Creating replayer PVC"
#  kubectl create -f
#set -e

echo "Starting Replayer Pod..."
kubectl apply -f  "$replayerPodYaml"
sleep 12
kubectl get pod -n mcm-ca-team

targetDBPath="${POD_NAME}:/db/${DB_NAME}"
targetReportDir="${POD_NAME}:/reports/"
echo "Copying db ${INPUT_DATA_PATH} to ${targetDBPath}..."
echo "kubectl cp -n mcm-ca-team ${INPUT_DATA_PATH} ${targetDBPath}"
kubectl cp -n mcm-ca-team "${INPUT_DATA_PATH}" "${targetDBPath}"
echo "Copy done. ${POD_NAME} should now commence work and will produce CA replay reports within dir ${targetReportDir}"
