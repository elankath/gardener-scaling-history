#!/usr/bin/env bash
set -eo pipefail

echoErr() { echo "$@" 1>&2; }

# Function to clean up background process
cleanup() {
    echo "Cleaning up..."
    if [[ -n "$kvclpid" ]]; then
        kill "$kvclpid" 2>/dev/null
        wait "$kvclpid" 2>/dev/null
        echo "Background process with PID $kvclpid terminated."
    fi
    if [[ -n "$srpid" ]]; then
        kill "$srpid" 2>/dev/null
        wait "$srpid" 2>/dev/null
        echo "Background process with PID $srpid terminated."
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

waitkvcl=8
waitsr=5
if [[ "$replaySR" == "true" ]] && [[ "${NO_AUTO_LAUNCH}" == "true" ]]; then
  export BINARY_ASSETS_DIR="/bin"
  echo "Launching kvcl..."
  /bin/kvcl 2>&1 | tee /tmp/kvcl.log &
  kvclpid=$!
  echo "Launched kvcl with pid: ${kvclpid}"
  echo "waiting ${waitkvcl}s for kvcl to start..."
  sleep ${waitkvcl}
  echo "Launching scaling-recommender with provider: ${provider} for INPUT_DATA_PATH: ${INPUT_DATA_PATH}..."
  /bin/scaling-recommender --provider="${provider}" --target-kvcl-kubeconfig="/tmp/kvcl.yaml" 2>&1 | tee /tmp/sr.log &
  srpid=$!
  echo "Launched sr with pid: ${srpid}"
  echo "waiting ${waitsr}s for sr to start..."
  sleep ${waitsr}
  if ps -p $srpid > /dev/null; then
      echo "Scaling recommender process with PID $pid is running."
  else
      echoErr "ERROR: scaling recommender process with PID $pid is not running."
      cat /tmp/sr.log >&2
      exit 1
  fi
fi

echo "Launching replayer..."
/bin/replayer 2>&1 | tee /tmp/replayer.log

if [[ -f /tmp/kvcl.log ]]; then
  curl -v -X POST -F logs=@/tmp/kvcl.log "http://10.47.254.238/api/logs/${clusterName}"
fi
if [[ -f /tmp/sr.log ]]; then
  curl -v -X POST -F logs=@/tmp/sr.log "http://10.47.254.238/api/logs/${clusterName}"
fi
if [[ -f /tmp/replayer.log ]]; then
  curl -v -X POST -F logs=@/tmp/replayer.log "http://10.47.254.238/api/logs/${clusterName}"
fi
