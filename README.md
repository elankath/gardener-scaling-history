# gardener-scaling-history

> [!NOTE]
> Presently, this is 🚧 WIP prototype for Proof of Concept only.

Repository providers 2 apps:
- recorder: monitors and records the scaling data for one or more gardner clusters: machine deployments, machine classes, scheduled and unscheduled pods, scaled nodes, etc. Recorded data is dumped into a [SQLite](https://sqlite.org/) DB per cluster.
- replayer: replays the recorded scaling data of a gardener cluster against another k8s cluster (set via `KUBECONFIG`).
  - effectively deploys scheduled and un-scheduled pods in a configurable batch interval and waits till nodes belonging to CA node groups are scaled.
  - This is primarily meant for simulating autoscaling  and hence ideally should be run against a virtual cluster like one setup by https://github.com/unmarshall/kvcl/

## Usage

TODO: describe pre-requisites.

Execute `go run cmd/recorder/main.go`



- replayer: compute starttime -> intial -> recordtime + delay ; else -> previousStarttime + batchinterval
- replayer: get the clusterSnapshot for initial time
- replayer: get the ca config from snapshot
- replayer: write it to `virtualAutoscalerConfigPath`
- autoscaler: performs refresh activity
- replayer:
  - find nodes delta (deleted nodes and new nodes created)
  - find scheduled pods delta
  - find unscheduled pods delta
  - compute `deltaClusterSnapshot` using above info.
  - if delta = 0 continue to new batch
  - replayer applies delta snapshot to virtual cluster
  - replayer waits for stabilize interval
- autoscaler: performs scaling activity (if any)
- replayer: generate scenario report
