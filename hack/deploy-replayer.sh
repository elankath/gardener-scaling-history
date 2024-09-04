#!/usr/bin/env bash
set -eo pipefail

echoErr() { echo "$@" 1>&2; }

if [[ -z "$DOCKERHUB_USER" ]]; then
  echoErr "Please export DOCKERHUB_USER var before executing this script and ensure you have logged in using 'docker login'"
  exit 1
fi

if [[ ! -f specs/replayer.yaml ]]; then
  echoErr "Please ensure that you are in the base dir of the gardener-scaling-history repo before running this script"
  exit 2
fi
export INPUT_DATA_PATH="/db/live_hc-eu30_prod-gc-haas.db"
echo "Please ensure you have used gardenctl to log into the right shoot cluster"
replayerJobYaml="/tmp/scaling-history-replayer.yaml"
envsubst < specs/replayer.yaml > "$replayerJobYaml"
echo "Substituted env variables in specs/replayer.yaml and wrote to $replayerJobYaml"
#kubectl delete -f "$replayerJobYaml" || echo "NOTE: recorder pods not already deployed."
#kubectl delete cm -n robot scaling-history-recorder-config || echo "NOTE: recorder config not already deployed."

#waitSecs=8
#echo "cleared objects..waiting for $waitSecs seconds before deploying fresh objects..."
#sleep "$waitSecs"
kubectl delete job -n robot scaling-history-replayer || echo "scaling-history-replayer JOB not yet deployed."
kubectl apply -f  "$replayerJobYaml"