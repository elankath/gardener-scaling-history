#!/usr/bin/env bash
set -eo pipefail

echoErr() { echo "$@" 1>&2; }

killProcess() {
  local name="$1"
  echo "getting PIDs for process $name"
  local pids=$(pgrep -f "$name")
  for pid in $pids; do
    echo "killing $pid..."
    kill -9 "$pid"
  done
}

killProcess "kube-apiserver"
killProcess "etcd"
killProcess "cluster-autoscaler"
killProcess "kvcl"
killProcess "scaling-recommender"




