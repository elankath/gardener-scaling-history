#!/usr/bin/env bash
set -eo pipefail

echoErr() { echo "$@" 1>&2; }

if [[ -z "$DOCKERHUB_USER" ]]; then
  echoErr "Please export DOCKERHUB_USER var before executing this script and ensure you have logged in using 'docker login'"
  exit 1
fi

if [[ ! -f specs/shr.yaml ]]; then
  echoErr "Please ensure that you are in the base dir of the gardener-scaling-history repo before running this script"
  exit 2
fi

echo "Please ensure you have used gardenctl to log into the right shoot cluster"
recorderPoYaml="/tmp/shr.yaml"
envsubst < specs/shr.yaml > "$recorderPoYaml"
echo "Substituted env variables in specs/recorder-pod.yaml and wrote to $recorderPoYaml"
kubectl delete -f "$recorderPoYaml" || echo "NOTE: recorder pods not already deployed."
kubectl delete cm -n robot scaling-history-recorder-config || echo "NOTE: recorder config not already deployed."
kubectl create cm -n robot scaling-history-recorder-config --from-file=cfg/clusters.csv
kubectl apply -f  "$recorderPoYaml"