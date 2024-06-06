#!/usr/bin/env zsh
set -eo pipefail


echoerr() { echo "$@" 1>&2; }

if [[ -z "$NSMALL" ]]; then
  echo "please define env N_SMALL number of pods to deploy"
  exit 1
fi
if [[ -z "$NMEDIUM" ]]; then
  echo "please define env N_MEDIUM number of pods to deploy"
  exit 1
fi
if [[ -z "$NLARGE" ]]; then
  echo "please define env N_LARGE number of pods to deploy"
  exit 1
fi

if [[ -z "$PARALLEL" ]]; then
  echo "Deploying $NLARGE pods, $NMEDIUM pods, $NSMALL serially..."
  for i in {1.."$NLARGE"}; do kubectl create -f specs/largePod.yaml; done
  for i in {1.."$NMEDIUM"}; do kubectl create -f specs/mediumPod.yaml; done
  for i in {1.."$NSMALL"}; do kubectl create -f specs/smallPod.yaml; done
else
  echo "Deploying $NLARGE pods, $NMEDIUM pods, $NSMALL pods parallelly..."
  for i in {1.."$NLARGE"}; do kubectl create -f specs/largePod.yaml; done \
   &  for i in {1.."$NMEDIUM"}; do kubectl create -f specs/mediumPod.yaml; done \
   &  for i in {1.."$NSMALL"}; do kubectl create -f specs/smallPod.yaml; done
fi
