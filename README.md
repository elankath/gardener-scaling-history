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


ReplayInterval - 5m

lastsp (t1) -> 2 unsc pods, 2 nodes, 5 sc pods
currentsp (t2 = t1 + replayInterval) -> 0 unsc pods, 3 nodes, 7 sc pods
deltawork -> 0 unsc pods, 1 node , 2 sc pods


### Report Generation
- if last cluster snapshot, we skip the scenario
- CS at T1 -> 
  - n1, n2, n3 nodes
  - Unsc pods - a, b, c
  - Sch pods - d, e, f
- sync the CS.nodes at T1 with kvcl in replayer
- deploy the delta work
- wait for stabilize interval
- check for scaled up nodes n4,n5
- If scale up is there create scenario report and append it.

- CS at T2 ->
  - n2, n3, n4 , n5
  - Unsc pods - c, g
  - Sch pods - a, b, e, f, h

delwork -> CST2 - CST1
podsToDelete -> d
podsToDeploy -> g, h

n1, n2 ,n3


- Replay R1
- deltaWork -> p1, p2, p3 (pods to deploy unscheduled)
- na, nb scaled up nodes which runs pa, pb , pc
- p1, p2, p3 have nodenames 

- Replay R2
- scheduled pods - p1, p2, p3