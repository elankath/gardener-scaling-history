# gardener-scalehist

Scaling History Recorder and Analyzer for K8s Gardener Clusters

> [!NOTE]
> This is WIP prototype for Proof of Concept. DO NOT USE!




## SCRATCH


2 Node groups: A, B
PODS: X1A, X2A, Y1A, Y2B


Check out

```go
//t turns out that we could already modify the sharedInformer regardless of my patch, e.g.
sharedInformer.InformerFor(&corev1.Pod{}, func(cli kubernetes.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
// create and return custom informer
})
sharedInformer.Start(stopCh

```

## Info Required from Seed
- Need the CA deployment
- Need the MCD object


### NodeGroups Ordering
- MCD-NG does not have min and max replicas count.
- Keep a MCD table to store mcd objects.
- Populate `nodegroup_history` with CA-NG.

### Scenario Report
- Represent a scale event for the nodegroup

#### Info required from nodes

- .status.allocatable.pods
- .status.capacity.pods

#### Analyzer

#### clusters.csv
landscape_name, shoot-namespace,shoot-yaml,seed-yaml

### TODO

- TSC Case for PendingUnscheduledPods
//After sceanario over
//Unsch : p5
//Nodes:  {n1,n2}
-
-
-
//{p6} -> {n3} -> trigger
//{p5 -> n1}

Case1 :
p6 -> n3
p5 -> n1 //assigned to TSC satisfied after p6

Case2 :
p6 -> n3
p5 -> n1 //scheduled event emitted after new workload deployed

- Put triggerScalingEvents[] in a map with time

### SCENARIO COALESCING EXAMPLE

#### ScaleUPx: Unschedulable Pods at T1 = {P1, P2}
NG-A scaled up

##### ScaleUpY: Unschedulable Pods at T2 = {P3, P4, P5}
NG-A scaled up by 1
NG-B scaled up by 1

PendingUnscheduledPods=p5

##### ScaleUpZ: Unschedulable Pods at T3 = {P5, P6, P7, P8}
NG-C scaled up by 1


Say: T2-T1 = 5m, T3-T1 = 55m

##### Coalescing
if you give COALESCE_INTERVAL=1m,
then you have 1 individual scenarios: ScaleUpX 

if you give COALESCE_INTERVAL=10m,
then you have 1 coalesced scenarios: ScaleUpX+ScaleUpY

if you give COALESCE_INTERVAL=60m,
then you have 1 coalesced scenarios: ScaleUpX+ScaleUpY+ScaleUpZ

### TODO:
- We want to deploy `scalehist-recorder` as a component in the control plane
due to viewer kubeconfig short expiry for control planes and IP whitelisting issues.

















