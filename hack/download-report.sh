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
echo "Downloading report list..."
reportList=$(curl http://10.47.254.238/api/reports | jq -r '.Items[].Name')
printf ">> Found report list \n: %s" $reportList
echo "Found reports: $reportList"
for reportName in ${(f)reportList}; do
  url="http://10.47.254.238/api/reports/$reportName"
  echo "Downloading report from url $url into tmp ..."
  curl -kL "$url" -o "/tmp/$reportName"
done