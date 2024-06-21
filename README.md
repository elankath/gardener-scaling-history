# gardener-scaling-history

Repository providers 2 apps:
- recorder: monitors and records the scaling data for a gardner cluster: machine deployments, machine classes, scheduled and unscheduled pods, scaled nodes, etc
- replayer: replays the recorded scaling data of a gardener cluster against another k8s cluster.
  - effectively deploys scheduled and un-scheduled pods in a batch interval and waits till nodes belonging to CA node groups are scaled.
  - This is meant for autoscaling simulation and hence ideally should be run against a virtual cluster like one setup by https://github.com/unmarshall/kvcl/

> [!NOTE]
> Presently, this is 🚧 WIP prototype for Proof of Concept only.

## Usage

TODO: describe pre-requisites.

Execute `go run cmd/recorder/main.go`
















