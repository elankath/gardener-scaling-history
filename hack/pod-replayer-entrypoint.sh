#!/usr/bin/env bash
set -eo pipefail

echoErr() { echo "$@" 1>&2; }

# Function to clean up background process
cleanup() {
    echo "Cleaning up..."
    if [[ -n "$pidKVCL" ]]; then
        kill "$pidKVCL" 2>/dev/null
    fi
    if [[ -n "$pidSR" ]]; then
        kill "$pidSR" 2>/dev/null
    fi
}

# Trap the EXIT signal to ensure cleanup is called when the script finishes
trap cleanup EXIT

inputDataPath=${INPUT_DATA_PATH}
inputDataFileName=$(basename "${inputDataPath}")
# Check if the string ends with the suffix
if [[ "$inputDataPath" == *".json" ]]; then
  replaySR="true"
  clusterName="${inputDataFileName%_*}"
else
  replaySR="false"
  clusterName="${inputDataFileName%.*}"
fi

echo "Computed variables: replaySR = ${replaySR}; clusterName = ${clusterName}"

declare provider

if [[ "$inputDataPath" == *"gcp"* ]] || [[ "$inputDataPath" == *"-gc-"* ]] || [[ "$inputDataPath" == *"_abap_"* ]]; then
  provider="gcp"
else
  provider="aws"
fi

waitKvcl=8
waitSR=8
if [[ "$replaySR" == "true" ]] && [[ "${NO_AUTO_LAUNCH}" == "true" ]]; then
  export BINARY_ASSETS_DIR="/bin"
  echo "NO_AUTO_LAUNCH set: Launching kvcl..."
  /bin/kvcl 2>&1 | tee /tmp/kvcl.log &
  pidKVCL=$!
  echo "Launched kvcl with pid: ${pidKVCL}"
  echo "waiting ${waitKvcl}s for kvcl to start..."
  sleep ${waitKvcl}
  echo "NO_AUTO_LAUNCH set: Launching scaling-recommender with provider: ${provider} for INPUT_DATA_PATH: ${INPUT_DATA_PATH}..."
  /bin/scaling-recommender --provider="${provider}" --target-kvcl-kubeconfig="/tmp/kvcl.yaml" 2>&1 | tee /tmp/sr.log &
  pidSR=$!
  echo "Launched scaling-recommender with pid: ${pidSR}"
  echo "waiting ${waitSR}s for sr to start..."
  sleep ${waitSR}
  if ps -p $pidSR > /dev/null; then
      echo "Scaling recommender process with PID $pidSR is running."
  else
      echoErr "ERROR: scaling recommender process with PID $pidSR is not running."
      cat /tmp/sr.log >&2
      exit 1
  fi
fi

declare replayerLogFileName
if [[ "$replaySR" == "true" ]] ; then
  replayerLogFileName="replayer-sr.log"
else
  replayerLogFileName="replayer-ca.log"
fi
echo "Launching replayer..."
/bin/replayer 2>&1 | tee "/tmp/${replayerLogFileName}"

if [[ -f /tmp/kvcl.log ]]; then
  curl -v -X POST -F logs=@/tmp/kvcl.log "http://10.47.254.238/api/logs/${clusterName}"
fi
if [[ -f /tmp/sr.log ]]; then
  curl -v -X POST -F logs=@/tmp/sr.log "http://10.47.254.238/api/logs/${clusterName}"
fi
if [[ -f "/tmp/$replayerLogFileName" ]]; then
  curl -v -X POST -F logs=@/tmp/${replayerLogFileName} "http://10.47.254.238/api/logs/${clusterName}"
fi
if [[ -f "/tmp/scores.log" ]]; then
  curl -v -X POST -F logs=@/tmp/scores.log "http://10.47.254.238/api/logs/${clusterName}"
fi
exit 0