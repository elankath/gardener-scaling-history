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

function main(){
  parse_flags "$@"
  if [[ -z "${SHOOT}" ]]; then
    echo -e "Shoot has not been passed. Please provide Shoot either by specifying --shoot or -t argument"
    exit 1
  fi
  if [[ -z "${PROJECT}" ]]; then
    echo -e "Project has not been passed. Please provide Project either by specifying --project or -l argument"
    exit 1
  fi
  if [[ -z "${LANDSCAPE}" ]]; then
    echo -e "LANDSCAPE has not been passed. Please provide Project either by specifying --landscape or -p argument"
    exit 1
  fi

  kubeconfig_path="$(pwd)/gen"

  (
    gardenctl target --garden sap-landscape-${LANDSCAPE}
    eval $(gardenctl kubectl-env bash)
    SHOOT_NAME=${SHOOT}
    PROJECT_NAMESPACE="garden-${PROJECT}"

    echo ${SHOOT_NAME}

    tmpfile=$(mktemp)
    printf '{"spec":{"expirationSeconds":600000000}}' > "$tmpfile"

    kubectl create -f "$tmpfile" --raw "/apis/core.gardener.cloud/v1beta1/namespaces/${PROJECT_NAMESPACE}/shoots/${SHOOT_NAME}/viewerkubeconfig" | jq -r ".status.kubeconfig" | base64 -d | tee "${kubeconfig_path}/kubeconfig-${SHOOT_NAME}.yaml"

    SEED_NAME=$(kubectl get shoot -n ${PROJECT_NAMESPACE} ${SHOOT_NAME} -ojson | jq -r '.status.seedName')
    PROJECT_NAMESPACE=garden

    kubectl create -f "$tmpfile" --raw "/apis/core.gardener.cloud/v1beta1/namespaces/${PROJECT_NAMESPACE}/shoots/${SEED_NAME}/viewerkubeconfig" | jq -r ".status.kubeconfig" | base64 -d | tee "${kubeconfig_path}/kubeconfig-${SEED_NAME}.yaml"

    rm "$tmpfile"
    gardenctl target unset garden
  ) > /dev/null
  printf "Kubeconfigs for shoot and seed have been downloaded to ${kubeconfig_path}\n"


  #check if a row with shootname exists in the csv file
  if ! grep -q $SHOOT ./gen/clusters.csv
  then
    echo "$LANDSCAPE,shoot--${PROJECT}--${SHOOT},kubeconfig-garden-${PROJECT}-${SHOOT}.yaml,kubeconfig-garden-${SEED}.yaml\n" >> ./shoots.csv
  fi
}

main "$@"