#!/usr/bin/env bash
set -eo pipefail

echoErr() { echo "$@" 1>&2; }

if [[ -z "$DOCKERHUB_USER" ]]; then
  echoErr "Please export DOCKERHUB_USER var before executing this script and ensure you have logged in using 'docker login'"
  exit 1
fi

if [[ ! -f specs/recorder.yaml ]]; then
  echoErr "Please ensure that you are in the base dir of the gardener-scaling-history repo before running this script"
  exit 2
fi
echo "Please ensure you have access to the gardener live landscape!"
gardenctl target --garden sap-landscape-live --project garden-ops --shoot utility-int
echo "Getting scrt robot-gardens..."
kubectl get secret -n robot robot-gardens -oyaml > /tmp/robot-gardens.yaml
echo "Modifying scrt robot-gardens for mcm-ca-team ns..."
cat /tmp/robot-gardens.yaml | sed 's/namespace: robot/namespace: mcm-ca-team/; s/name: robot-gardens/name: gardens/; /resourceVersion/d; /uid/d; /creationTimestamp/d' > /tmp/gardens.yaml
kubectl apply -f /tmp/gardens.yaml



recorderPoYaml="/tmp/scaling-history-recorder.yaml"
envsubst < specs/recorder.yaml > "$recorderPoYaml"
echo "Substituted env variables in specs/recorder.yaml and wrote to $recorderPoYaml"
#kubectl delete -f "$recorderPoYaml" || echo "NOTE: recorder pods not already deployed."
kubectl delete -n mcm-ca-team po scaling-history-recorder || echo "NOTE: recorder pod is not already deployed."
kubectl delete cm -n mcm-ca-team scaling-history-recorder-config || echo "NOTE: recorder config not already deployed."

waitSecs=4
echo "cleared objects..waiting for $waitSecs seconds before deploying fresh objects..."
sleep "$waitSecs"
kubectl create cm -n mcm-ca-team scaling-history-recorder-config --from-file=cfg/clusters.csv
kubectl apply -f  "$recorderPoYaml"