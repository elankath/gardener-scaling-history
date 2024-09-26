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
  echo "INPUT_DATA_PATH NOT specified. Getting list of recorded reports.."
  echo "Executing kubectl port-forward -n mcm-ca-team pod/scaling-history-recorder 8080:8080..."
  kubectl port-forward -n mcm-ca-team pod/scaling-history-recorder 8080:8080 &
  pid=$!
  sleep 6
  echo "Started port-forwarding with PID: $pid"
  echo "Downloading report list..."
  reportList=$(curl localhost:8080/api/reports)
  printf ">> Found reports \n: %s" $reportList
  echo
  echo "Kindly Select a report for which to run the recommender to produce report:"
  reportList=$(echo "$reportList" | tr '\n' ' ')
  chosenReport=$(gum choose $reportList)
  echo "You have chosen $chosenReport ! Will run scaling-recommender against this report."
#  export INPUT_DATA_PATH="/data/reports/$chosenReport"
#  echo "INPUT_DATA_PATH has been set to $INPUT_DATA_PATH for scaling-recommender pod."
  #  export INPUT_DATA_PATH="/db/$chosenDb"
  export REPORT_NAME=${chosenReport:t}
else
  usage="Kindly download the replayCA report using ./hack/download-reports.sh and set the path to it in the INPUT_DATA_PATH env var"

  if [[ ! -f "$INPUT_DATA_PATH" ]]; then
    echoErr "replayCA report does not exist at $INPUT_DATA_PATH. ${usage}"
    exit 4
  fi

  if [[ "$INPUT_DATA_PATH" != *".json" ]]; then
    echoErr "file at $INPUT_DATA_PATH does not appear to be a json file. ${usage}"
    exit 5
  fi
  export REPORT_NAME=${INPUT_DATA_PATH:t}
fi

export SCALER="sr"
export POD_SUFFIX=$(print -P "%{$(echo $RANDOM | md5sum | head -c 3)%}")
export POD_NAME="scaling-history-replayer-${SCALER}-${POD_SUFFIX}"
export POD_DATA_PATH="/reports/${REPORT_NAME}"
echo "POD_DATA_PATH has been set to ${POD_DATA_PATH} for ${POD_NAME} pod."

replayerPodYaml="/tmp/scaling-history-replayer.yaml"
export NONCE="$(date)"
export SCALER="sr"
export MEMORY="16Gi"
envsubst < specs/replayer.yaml > "$replayerPodYaml"
echo "Substituted env variables in specs/replayer.yaml and wrote to $replayerPodYaml"
#kubectl delete job -n mcm-ca-team scaling-history-replayer || echo "scaling-history-replayer JOB not yet deployed."
sleep 1
echo "Starting Replayer Pod..."
kubectl create -f  "$replayerPodYaml"
sleep 2
kubectl get pod -n mcm-ca-team

targetReportPath="${POD_NAME}:/reports/${REPORT_NAME}"
targetReportDir="${POD_NAME}:/reports/"
echo "Copying replayCA report ${INPUT_DATA_PATH} to ${targetReportPath}..."
echo "kubectl cp -n mcm-ca-team ${INPUT_DATA_PATH} ${targetReportPath}"
kubectl cp -n mcm-ca-team "${INPUT_DATA_PATH}" "${targetReportPath}"
echo "Copy done. ${POD_NAME} should now commence work and will produce SR reports within dir ${targetReportDir}"