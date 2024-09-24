#!/usr/bin/env zsh
set -eo pipefail

echoErr() { echo "$@" 1>&2; }


landscapeName="$1"
if [[ -z "$1" ]]; then
  echo "Usage: ./hack/list-big-shoots.sh <landscapeName>"
  echo "landscapeName not provided, assuming live"
  landscapeName="live"
fi

echo "NOTE: Please ensure that you have CAM access to landscape: $landscapeName"
echo "Targeting $landscapeName..."
gardenctl target --garden sap-landscape-$landscapeName
echo "Listing projects of landscape $landscapeName ..."
#projects=$(kubectl get project | sed '1d' | cut -d $' ' -f 1)
projects=$(kubectl get project -o jsonpath='{.items[*].metadata.name}')
mkdir -p /tmp/shoot-infos
declare totalShoots=0
projectNames=("${(z)projects}")
echo "--------------Getting shoots for #$totalProjects in landscape $landscapeName..."
declare shootInfos
declare totalProjects=${#projectNames[@]}
declare allShootInfos
for p in "${projectNames[@]}"; do
  echo "Analyzing project $p in landscape $landscapeName..."
  shootInfosFile="/tmp/shoot-infos/${landscapeName}_${p}.json"
  if [[ "$p" == "agrirouter" ]]; then
    echo "Skipping $p..."
    continue
  fi
  if [[ -f "$shootInfosFile" ]]; then
    echo "Reading $shootInfosFile..."
    shootInfos=$(cat "$shootInfosFile")
    allShootInfos+="$shootInfos\n"
  else
    echo "Logging into project: $p..."
    gardenctl target --garden sap-landscape-$landscapeName --project $p
    echo "Getting shoot name/ns/numPools for project $p..."
    shootInfos=$(kubectl get shoot -ojson | \
      jq -c --arg projectName "$p" --arg landscapeName "$landscapeName" '.items[] | {name: .metadata.name,  namespace: .metadata.namespace, landscape: $landscapeName, project: $projectName, provider: .spec.provider.type, numPools: (.spec.provider.workers | length) }')
    #shootInfos=$(echo "$shootInfos" | jq -c .)
    echo "$shootInfos" > "$shootInfosFile"
    echo "Wrote shoot infos to $shootInfosFile"
    allShootInfos+="$shootInfos"
  fi
  numShootInfos=$(echo "$shootInfos" | jq -s 'length')
  totalShoots=$((totalShoots + numShootInfos))
  echo "Loaded #${numShootInfos} shoot infos of project $p into allShootInfos"
done
echo "Total Projects in landscape $landscapeName: $totalProjects"
echo "Total number of shoots: $totalShoots"
declare allShootInfosPath="/tmp/shoot-infos/${landscapeName}_all_shoot-infos.json"
echo "$allShootInfos" | jq -s 'sort_by(.numPools) | reverse' | jq -c '.[]' | sed '/^$/d' > "$allShootInfosPath"
echo "Wrote all shoot infos to $allShootInfosPath"

#declare topShootInfosPath="/tmp/shoot-infos/top-$limit-shoot-infos.json"
declare nonHanaShootInfosPath="/tmp/shoot-infos/${landscapeName}_top_non-hana-shoot-infos.json"
sed '/haas\|hna/d' <"$allShootInfosPath" > "$nonHanaShootInfosPath"
echo "Wrote top non-hana  shoot infos to $nonHanaShootInfosPath"
echo "Done."

