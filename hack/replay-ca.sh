#!/usr/bin/env bash
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
  export INPUT_DATA_PATH="/data/db/$chosenDb"
  echo "INPUT_DATA_PATH has been set to $INPUT_DATA_PATH for replayer job."
fi


#replayerDepsYaml="/tmp/scaling-history-replayer-deps.yaml"
#envsubst < specs/replayer-deps.yaml > "$replayerDepsYaml"
#echo "Substituted env variables in specs/replayer-deps.yaml and wrote to $replayerDepsYaml"
#sleep 1
#echo "Applying Replayer dependencies..."
#kubectl apply -f  "$replayerDepsYaml"

#export INPUT_DATA_PATH="/db/live_hc-eu30_prod-gc-haas.db"
replayerPodYaml="/tmp/scaling-history-replayer.yaml"
export NONCE="$(date)"
envsubst < specs/replayer.yaml > "$replayerPodYaml"
echo "Substituted env variables in specs/replayer.yaml and wrote to $replayerPodYaml"
#kubectl delete job -n mcm-ca-team scaling-history-replayer || echo "scaling-history-replayer JOB not yet deployed."
sleep 1
echo "Starting Replayer Pod..."
kubectl create -f  "$replayerPodYaml"
sleep 2
kubectl get pod -n mcm-ca-team
