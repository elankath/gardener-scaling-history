function main(){
  kubectl create secret generic kubeconfig --from-file=./gen
  for file in ./cfg/*; do
    if [[ $file == *".csv" ]]; then
      continue
    fi
    kubectl apply -f $file
  done
}

main "$@"
