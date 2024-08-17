# gardener-scaling-history

> [!NOTE]
>  This is prototype for Proof of Concept only.

Repository providers 2 apps:
- recorder: monitors and records the scaling data for one or more gardner clusters: machine deployments, machine classes, priority classes, autoscaling config, scheduled and unscheduled pods, nodes, etc. Recorded data is dumped into a [SQLite](https://sqlite.org/) DB per cluster.
- replayer: replays the recorded scaling data of a gardener cluster against another k8s cluster (set via `KUBECONFIG`).
  - effectively deploys scheduled and un-scheduled pods in a configurable batch interval and waits till nodes belonging to CA node groups are scaled.
  - This is primarily meant for simulating autoscaling  and hence ideally should be run against a virtual cluster like one setup by https://github.com/unmarshall/kvcl/

## Launch the Recorder

### Local Launch
1. Prepare `clusters.csv` for recordering. This is a CSV file with 3 columns: landscape name, project name, shoot name
    An example clusters.csv file for recording data for the `g2` cluster of project `i034796` in the `dev` landscape is shown below.
    ```clusters.csv
    dev,i034796,g2
    ```
1. Store `clusters.csv` in a directory `configDir`. 
1. Kindly set environment variables
   1. `export MODE=local` 
   1. `export CONFIG_DIR=<configDir>` # Dir of `clusters.csv`
   1. `export DB_DIR=/tmp` # Directory where recorder stores SQLite `<clusterName>.db` files
1. Execute: `go run cmd/recorder/main.go`
 
### Remote Launch
1. Login into `utility-int` cluster `gardenctl target --garden sap-landscape-live --project garden-ops --shoot utility-int`
1. Export your docker hub username: `export DOCKERHUB_USER=<dockerHubUser>`
1. Login into Docker Hub: `docker login -u $DOCKERHUB_USER -p <dockerHubPass>`
1. Run `./hack/build-recorder.sh`
1. Run `./hack/build-recorder.sh`

### Download Recorder Databases

1. Port forward the recorder Pod's 8080 port locally 
   1. `kubectl port-forward -n robot pod/scaling-history-recorder 8080:8080`
2. Use curl to list recorder SQLite DBs: 
   1. `curl localhost:8080/db`
   1. This will list the `.db` files
   ```
   live_hc-ap11_prod-haas.db
   live_hc-ap11_prod-hdl.db
   live_hc-ap11_prod-orc.db
   live_hc-canary_prod-haas.db
   live_hc-canary_prod-hna0.db
   live_hc-eu20_prod-az-haas.db
   live_hc-eu20_prod-az-orc.db
   ```
1. Use curl to download a specific DB 
   1.  `cd /tmp; curl -kLO localhost:8080/db/live_hc-ap11_prod-hdl.db`
1. End the port-forwarding.
1. Use any DB Browser of your choice to open downloaded DB
   
   


## Launch the Re-player
1. Launch the Virtual cluster. 
1. `go run cmd/replayer/main.go`


