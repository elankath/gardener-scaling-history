#!/usr/bin/env zsh
set -eo pipefail


echoerr() { echo "$@" 1>&2; }

if [[ -z "$NA" ]]; then
  echo "please define env N_A number of pods to deploy"
  exit 1
fi
if [[ -z "$NB" ]]; then
  echo "please define env N_B number of pods to deploy"
  exit 1
fi
if [[ -z "$NC" ]]; then
  echo "please define env N_C number of pods to deploy"
  exit 1
fi
if [[ -z "$ND" ]]; then
  echo "please define env N_D number of pods to deploy"
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
