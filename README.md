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
