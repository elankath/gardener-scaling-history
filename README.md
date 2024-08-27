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

### Download Automatically into /tmp dir

1. Ensure you have garden live landscape access.
1. Kindly run the script `./hack/download-db.sh`

### Download Manually
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
   
   

## Launch the Replayer

The replayer operates replays scenarios and generates a report. It needs a `INPUT_DATA_PATH` which is a path
to the recorded DB Path which can also be a generated scenario json.

In either case it generates another scenario report.

### Launch Replayer to generate scenario from recorded DB

1. Launch the virtual cluster: KVCL. 
1. Export `INPUT_DATA_PATH`. Ex: `export  INPUT_DATA_PATH=dev_i034796_g2.db`
1. (optional) You may also optionally change default `STABILIZE_INTERVAL` of `1m`. . Ex: `export STABILIZE_INTERVAL=25s`
1. (optional) You may also optionally change default of `/tmp` for the scenario `REPORT_DIR`. Ex: `REPORT_DIR=/tmp`
1. (optional) You may also optionally change default of `/tmp/kvcl.yaml` for the virtual `KUBECONFIG`. Ex: `KUBECONFIG=cfg/virtual-kubeconfig.yaml`
1. Execute replayer:  `go run cmd/replayer/main.go`
