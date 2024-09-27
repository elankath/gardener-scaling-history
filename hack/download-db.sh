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
if [[ -z "$DOWNLOAD_DB" ]]; then
  echo "Downloading db list..."
  dbList=$(curl http://10.47.254.238/api/db | jq -r '.Items[].Name')
  echo "Found databases: $dbList"
else
  dbList="$DOWNLOAD_DB"
  echo "Downloading $dbList"
fi
for dbName in ${(f)dbList}; do
  url="http://10.47.254.238/api/db/$dbName"
  echo "Downloading DB from url $url into gen ..."
  curl -kL "$url" -o "gen/$dbName"
done