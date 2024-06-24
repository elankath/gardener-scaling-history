function parse_flags() {
  while test $# -gt 0; do
    case "$1" in
    --shoot | -t)
      shift
      SHOOT="$1"
      ;;
    --project | -p)
      shift
      PROJECT="$1"
      ;;
    --landscape | -l)
      shift
      LANDSCAPE="$1"
      ;;
    esac
    shift
  done
}

echoErr() { echo "$@" 1>&2; }

function main() {
  parse_flags "$@"
  if [[ -z "${SHOOT}" ]]; then
    echoErr "Shoot has not been passed. Please provide Shoot either by specifying --shoot or -t argument"
    exit 1
  fi
  if [[ -z "${PROJECT}" ]]; then
    echoErr "Project has not been passed. Please provide Project either by specifying --project or -p argument"
    exit 1
  fi
  if [[ -z "${LANDSCAPE}" ]]; then
    echoErr "LANDSCAPE has not been passed. Please provide Project either by specifying --landscape or -l argument"
    exit 1
  fi

  local genPath
  genPath="$(pwd)/gen"

  gardenctl target --garden "sap-landscape-${LANDSCAPE}"
  eval "$(gardenctl kubectl-env bash)"
  SHOOT_NAME=${SHOOT}
  PROJECT_NAMESPACE="garden-${PROJECT}"

  local shootSpecFile
  shootSpecFile=$(mktemp)
  printf '{"spec":{"expirationSeconds":600000000}}' > "$shootSpecFile"

  local shootKubeConfigPath
  shootKubeConfigPath="${genPath}/kubeconfig-${PROJECT_NAMESPACE}-${SHOOT_NAME}.yaml"
  echo "Generating viewerkubeconfig for shoot into $shootKubeConfigPath ..."
  kubectl create -f "$shootSpecFile" --raw "/apis/core.gardener.cloud/v1beta1/namespaces/${PROJECT_NAMESPACE}/shoots/${SHOOT_NAME}/viewerkubeconfig" | jq -r ".status.kubeconfig" | base64 -d > "$shootKubeConfigPath"

  local shootJsonPath
  shootJsonPath=$(mktemp)
  echo "executing command: kubectl get shoot -n ${PROJECT_NAMESPACE} ${SHOOT_NAME} -o json"
  kubectl get shoot -n "${PROJECT_NAMESPACE}" "${SHOOT_NAME}" -o json > "$shootJsonPath"
  SEED_NAME=$(jq -r '.status.seedName' "$shootJsonPath")
  PROJECT_NAMESPACE=garden

  local seedKubeConfigPath
  seedKubeConfigPath="${genPath}/kubeconfig-$PROJECT_NAMESPACE--$SEED_NAME.yaml"

  echo "Generating viewerkubeconfig for shoot's control plane (seed) into $seedKubeConfigPath ..."
  kubectl create -f "$shootSpecFile" --raw "/apis/core.gardener.cloud/v1beta1/namespaces/${PROJECT_NAMESPACE}/shoots/${SEED_NAME}/viewerkubeconfig" | jq -r ".status.kubeconfig" | base64 -d > "$seedKubeConfigPath"


  rm "$shootSpecFile"
  rm "$shootJsonPath"
  #gardenctl target unset garden
  #
  printf "Kube configs for shoot and seed have been downloaded to %s\n"  "${genPath}"


  local clusterCsvPath
  clusterCsvPath="${genPath}/clusters.csv"
  SHOOT_NAMESPACE="shoot--$PROJECT--$SHOOT_NAME"
  #check if a row with the SHOOT_NAME exists in the csv file
  if ! grep -q "$SHOOT_NAME" ./gen/clusters.csv; then
    printf "%s,%s,%s,%s\n" "$LANDSCAPE" "$SHOOT_NAMESPACE" "$shootKubeConfigPath" "$seedKubeConfigPath" >> "$clusterCsvPath"
  fi

}

main "$@"