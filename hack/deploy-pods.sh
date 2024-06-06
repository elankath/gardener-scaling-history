#!/usr/bin/env zsh
set -eo pipefail


echoerr() { echo "$@" 1>&2; }

NA=3
NB=2
NC=2
ND=1
#PARALLEL=true

if [[ -z "$PARALLEL" ]]; then
  echo "Deploying $NA A pods, $NB B pods, $NC C pods, $ND D pods serially..."
  for i in {1.."$NA"}; do kubectl create -f specs/a.yaml; done
  for i in {1.."$NB"}; do kubectl create -f specs/b.yaml; done
  for i in {1.."$NC"}; do kubectl create -f specs/c.yaml; done
  for i in {1.."$ND"}; do kubectl create -f specs/d.yaml; done
else
  echo "Deploying $NA A pods, $NB B pods, $NC C pods, $ND D pods parallelly.."
  for i in {1.."$NA"}; do kubectl create -f specs/a.yaml; done \
   &  for i in {1.."$NB"}; do kubectl create -f specs/b.yaml; done \
   &  for i in {1.."$NC"}; do kubectl create -f specs/c.yaml; done \
   &  for i in {1.."$ND"}; do kubectl create -f specs/d.yaml; done
fi
