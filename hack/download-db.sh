#!/usr/bin/env zsh
set -eo pipefail


echoerr() { echo "$@" 1>&2; }

# Function to clean up background process
cleanup() {
    echo "Cleaning up..."
    if [[ -n "$pid" ]]; then
        kill "$pid" 2>/dev/null
        wait "$pid" 2>/dev/null
        echo "Background process with PID $pid terminated."
    fi
}

echo "NOTE: Please ensure you have Gardener Live Landscape Access"
gardenctl target --garden sap-landscape-live --project garden-ops --shoot utility-int

# Set up trap to call cleanup function on script exit or interrupt
trap cleanup EXIT
echo "Executing  kubectl port-forward -n robot pod/scaling-history-recorder 8080:8080..."
kubectl port-forward -n robot pod/scaling-history-recorder 8080:8080 &
pid=$!
sleep 7
echo "Started port-forwarding with PID: $pid"
echo "Downloading db list..."
dbList=$(curl localhost:8080/db)
echo "Found databases: $dbList"
for dbName in ${(f)dbList};  do
  url="http://localhost:8080/db/$dbName"
  echo "Downloading DB from url $url into gen ..."
  curl -kL "$url" -o "gen/$dbName"
done