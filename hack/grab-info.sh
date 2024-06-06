#!/usr/bin/env bash

SHOOT_NAMESPACE="shoot--i585976--target-gcp"
#check if file exists
if [ -f /tmp/datalog.txt ]; then
  rm /tmp/datalog.txt
fi

while true; do
  printf '################%s###############\n' "$(date '+%Y-%m-%d %H:%M:%S')" >> /tmp/datalog.txt 2>&1
  export KUBECONFIG=./gen/kubeconfig-gcp-ha.yaml
  kubectl get mcd -n $SHOOT_NAMESPACE >> /tmp/datalog.txt 2>&1
  printf '###############\n' >>  /tmp/datalog.txt  2>&1
  export KUBECONFIG=./gen/kubeconfig-target-gcp.yaml
  kubectl get pods -A -owide >>  /tmp/datalog.txt 2>&1
  kubectl get nodes >> /tmp/datalog.txt 2>&1

  function kubectlevents() {
   {
   echo $'TIME\tNAMESPACE\tTYPE\tREASON\tOBJECT\tSOURCE\tMESSAGE';
   kubectl get events -o json "$@" | jq -r '.items | map(. + {t: (.eventTime//.lastTimestamp)}) | sort_by(.t)[] | [.t, .metadata.namespace, .type, .reason, .involvedObject.kind + "/" + .involvedObject.name, .source.component + "," + (.source.host//"-"), .message] | @tsv';
   } | column -s $'\t' -t
  }

  kubectlevents | tail -20 >> /tmp/datalog.txt 2>&1
  printf "Appended logs to /tmp/datalog.txt\n"
  sleep 15
done