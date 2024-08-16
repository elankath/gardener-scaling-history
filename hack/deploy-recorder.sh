#!/usr/bin/env bash
set -eo pipefail

echoErr() { echo "$@" 1>&2; }

echo "Please make sure you have logged into your docker hub account using 'docker login' \
and set the TAG env for the image tag before running this script"

echo "Please ensure your image in spec/recorder-pod.yaml is correct"

echo "Please ensure you have used gardenctl to log into the right shoot cluster"

kubectl delete -f specs/recorder-pod.yaml || echo "recorder pods not yet deployed."

kubectl delete cm -n robot scaling-history-recorder-config || echo "recorder config not already deployed. Will deploy again."

kubectl create cm -n robot scaling-history-recorder-config --from-file=cfg/clusters.csv

kubectl apply -f specs/recorder-pod.yaml