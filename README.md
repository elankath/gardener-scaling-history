# gardener-scaling-history

> [!NOTE]
>  This is a prototype for PROOF OF CONCEPT *only*.

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


##### Virtual Cluster Autoscaler Replayer

* Virtual Cluster Autoscaler is a fork of the k8s `cluster-autoscaler` with a *virtual* provider implementation that scales virtual nodes on the virtual cluster
  
High level overview of the replay loop for the virtual CA replay run.

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
-->DeployWorkload["<div>Deploy Pods, Namespaces</div>"]
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

High level overview of the replay loop for the scaling recommender replay run

```mermaid
graph TB

subgraph vca["Scaling Recommender"]
ReceiveClusterSnapshot
-->GenerateScoresForGroupAndZone
-->FindWinner
-->GenerateScoresForGroupAndZone
-->UntilNoUnscheduledPods
-->ScaleVirtualNodes
-->SendRecommenderResponse


end

subgraph sr_replayer["SR Replayer Loop"]
ReadCAReport
-->LoadClusterSnapshotInCAReport
-->SynchronizeVirtualNodes
-->DeployWorkload["<div>Deploy Pods, Namespaces</div>"]
-->WaitForStabilizeInterval
-->PostClusterSnapshot
-->ReceiveRecommenderResponse
-->CreateAndWriteScalingReport
-->LoopEnd(((ReplayLoopEnd)))
end


subgraph kvcl["Virtual Cluster"]
Pods
Nodes
end

SendRecommenderResponse-->ReceiveRecommenderResponse
ScaleVirtualNodes-->kvcl
SynchronizeVirtualNodes-->kvcl
PostClusterSnapshot-->ReceiveClusterSnapshot


```

### Deployment model
![deployment model](web/poc.png)


