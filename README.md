# gardener-scaling-history

> [!NOTE]
>  This is a prototype for PROOF OF CONCEPT *only*.

Repository providers 2 apps:
- replayer: The replayer can replay scenarios against either the virtual CA or the scaling recommender
  - replay-CA: replays the recorded scaling data of a gardener cluster against a virtual k8s cluster.
       - effectively deploys scheduled and un-scheduled pods in a configurable batch interval and waits till nodes belonging to CA node groups are scaled.
       - This is primarily meant for simulating autoscaling  and hence ideally should be run against a virtual cluster like one setup by https://github.com/unmarshall/kvcl/
  - replay-SR: replays the scenario json (produced by replay-CA) against the new scaling-recommender.

## Setup

Please refer to the [setup guide](docs/setup.md)

## Architecture

### Design

#### Recorder

- The `scaling-history-recorder` monitors a list of clusters defined inside a config map 
`scaling-history-recorder-config` and setups up [client-go Shared Informers](https://pkg.go.dev/k8s.io/client-go/informers) 
on the data and control plane of those clusters monitoring `Pod`, `Node`, `Events`, `MachineDeployments`, `MachineClasses` and CA `Deployment` 
and `ConfigMap`. 
- Recorded data is stored into a [SQLite](https://sqlite.org/) DB per cluster. Care is taken to minimize amount of data stored - skipping storage if no scaling relevant change is made


```mermaid
graph TB;
    sqlitedb[(ClusterDB)]
    recorder[<div style='text-align:left'>
    <h4 style='margin:0px;padding:0'>recorder</h4>
    Listens to event callbacks
    Persists to clusterDB
    ]
    shared_informers[<div style='text-align:left;margin-top:0;padding-top:0'>
    <h4 style='margin:0px;padding:0'>shared_informers</h4>
     Control Plane Informer for MCC, MCD, etc
     Data Plane Informers for Pods, Nodes, etc
    </div>]
    shared_informers-->recorder;
    recorder-->sqlitedb

```

#### Replayer

* The replayer can replay scenarios against either the *VCA* (Virtual Cluster Autoscaler)
 or the *SR* (scaling recommender)
* This is primarily meant for simulating autoscaling  and hence ideally should be run against a virtual cluster like one setup by https://github.com/unmarshall/kvcl/


##### Virtual CLuster Autoscaler Replayer

```mermaid
graph TB

subgraph vca["Virtual Cluster Autoscaler"]
CARunLoop
-->ScalingOrchestrator
-->IncreaseNodeGroupSize
-->ScalingDone
CloudProviderRefresh
-->ReadAutoScalerConfig
-->CreateVirtualNodes
end

BuildAutoScalerConfig-->ReadAutoScalerConfig
subgraph ca_replayer["CA Replayer Loop"]
sqlitedb-->GetScalingEvent
GetScalingEvent
-->GetClusterSnapshotAtScalingEventTime
-->BuildAutoScalerConfig
CARefreshed
-->DeployWorkload["<div>Deploy Pods, Namespaces, Priority Classes
</div>"]
-->WaitForVirtualScaling
-->DoneSignalReceived
-->CreateAndWriteScalingReport
-->RepeatLoop(((CALoopEnd)))
end


subgraph kvcl["Virtual Cluster"]
Pods
Nodes
end


IncreaseNodeGroupSize-->kvcl
CreateVirtualNodes-->kvcl
CreateVirtualNodes-->CARefreshed
ScalingDone-->DoneSignalReceived
```

##### Scaling Recommender Replayer

### Deployment model
![deployment model](web/poc.png)


