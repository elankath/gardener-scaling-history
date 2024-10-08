#!/usr/bin/env zsh
set -eo pipefail


echoerr() { echo "$@" 1>&2; }

# Set up trap to call cleanup function on script exit or interrupt
echo "Downloading report list..."
reportList=$(curl http://10.47.254.238/api/reports | jq -r '.Items[].Name')
reportList=$(echo "$reportList" | tr '\n' ' ')
chosenReports=$(gum choose --height=40 --no-limit "${=reportList}")
#printf ">> Found report list \n: %s" $reportList
#echo "Found reports: $reportList"
for reportName in ${(f)chosenReports}; do
  url="http://10.47.254.238/api/reports/$reportName"
  echo "Downloading report from url $url into tmp ..."
  curl -kL "$url" -o "/tmp/$reportName"
done