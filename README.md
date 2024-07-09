# gardener-scaling-history

> [!NOTE]
> Presently, this is 🚧 WIP prototype for Proof of Concept only.

Repository providers 2 apps:
- recorder: monitors and records the scaling data for one or more gardner clusters: machine deployments, machine classes, autoscaling config, scheduled and unscheduled pods, nodes, etc. Recorded data is dumped into a [SQLite](https://sqlite.org/) DB per cluster.
- replayer: replays the recorded scaling data of a gardener cluster against another k8s cluster (set via `KUBECONFIG`).
  - effectively deploys scheduled and un-scheduled pods in a configurable batch interval and waits till nodes belonging to CA node groups are scaled.
  - This is primarily meant for simulating autoscaling  and hence ideally should be run against a virtual cluster like one setup by https://github.com/unmarshall/kvcl/

## Launch the Recorder

1. `go run cmd/recorder/main.go`


## Lauch the Re-player
1. Launch the Virtual cluster. 
1. `go run cmd/replayer/main.go`


