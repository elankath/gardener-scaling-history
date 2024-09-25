#!/usr/bin/env bash
set -eo pipefail

echoErr() { echo "$@" 1>&2; }

if [[ ! -f specs/app.yaml ]]; then
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


appPoYaml="/tmp/scaling-history-app.yaml"
if [[ -f "$appPoYaml" ]]; then
  echo "Removing existing, old $appPoYaml"
  rm "$appPoYaml"
fi
envsubst < specs/app.yaml > "$appPoYaml"
echo "Substituted env variables in specs/app.yaml and wrote to $appPoYaml"
exit 0
kubectl delete -n mcm-ca-team po scaling-history-app || echo "NOTE: scaling-history-app pod is not already deployed."

waitSecs=4
echo "cleared objects..waiting for $waitSecs seconds before deploying fresh objects..."
sleep "$waitSecs"
kubectl apply -f  "$appPoYaml"