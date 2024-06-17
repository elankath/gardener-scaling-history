### virtual-cluster (kvcl) (suyash)
- [ ]  Ask madhav to give us admin access.
- [ ] We move `scalingrecommender.virtualenv` to this project `kvcl`
### virtual-cluster-autoscaler (kvcl-autoscaler)
- [ ] This will be a fork of the CA with a virtual provider implementation based on the `kvcl`.
- [ ] We can code this together.
- [ ] When the cluster-autoscaler binary is launched (as process) or launched via facade , it operates against the virtual cluster `kvcl`
### gardener-scalehist
#### scalehist.Recorder
- [ ] Persist `MachineDeployment` history
- [ ] Persist `WorkerPool` history.
- [ ] Persist `Node` history.
- [ ] Persist `Pod` history.
- [ ] Will run as web-app. And you can download db at any time to run the local CLI replayer against cluster history.
#### scalehist.Replayer
- [ ] Depends on `kvcl-scaler` and `kvcl`
- [ ] Leverages the DB created by the recorder to replay scaling virtually
- [ ] Consumer (which is the `scaling-recommender` ), creates this replayer using `scalehist.NewReplayer`
```
func NewReplayer(dbPath clusterDb) Replayer {
// it will setup a virtual cluster with Nodes+NodeGroups+scheduledPods
// Then it will launch the CA against the kvcl..
// But does NOT deploy unscheduled Pods as YET
}
```   

Example of how consumer (`scaling-recommender`) uses the `Replayer`
- Initialize the replayer using `NewReplayer`. (This only setups up VC, CA, disables scale-down and populates Nodes at start time and populates scheduled Pods at the start time)
- Consumer should call `replayer.PlayBack(duration)` . How does the consumer figure out what duration to pass ?  
	- The consumer controls this. 
- Proposed Approach for replaying:
	- `Replayer.Playback(duration, batchInterval)`: We go through the Pods from startTime->endTime=startTime+duration
		- Takes the starting `ClusterSnapshot`
		- Then pick up the Pods in `batchTime`. 
			- batchInterval= CA scan period by default.
		- Wait till cluster stabilizes. (CA and scheduler has finished)
		- Continue in this way until endTime is reached.
		- Takes the ending `ClusterSnapshot`
		- Returns (atStart, atEnd) clustersnapshots

```go
type Replayer interface {  
  Replay(Duration duration) (ReplayReport)
  getRecordStartTime() Time 
  getRecordTime()  Time
  getClusterAt(time time.Time) ClusterSnapshot  // just for convenience.
  //getScalingEvents(t1, t2)  
}

type ReplayReport struct {
	Start ClusterSnapshot
	End   ClusterSnapshot
	BatchInterval time.Duration
    ScaledUpNodeGroups  map[string]int
    ScaledUpNode  []scalehist.NodeInfo
    DeployedPods [][]scalehist.PodInfo // pods that were deployed between start and end in ascending order seperated by batch interval.
     // PodInfo should have DeletionTimestamp.
}

type ClusterSnapshot struct {
	UncheduledPods []scalehist.PodInfo
	ScheduledPods []scalehist.PodInfo
 	NominatedPods []scalehist.PodInfo
	Nodes []scalehist.NodeInfo
    WorkerPool []WorkerPoolInfo
	NodeGroups []NodeGroupInfo // lean and mean NodeGroupInfo
    MachineDeploymentInfo []MachineDeploymentInfo
	CASettings CASettingsInfo
}
```
