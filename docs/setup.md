# Setup Guide

This document is meant to be a how-to guide on setting up:

* A recorder which captures live data from clusters.
* A replayer which creates scenarios out of the captured data and replays them onto a virtual CA which runs as part of Virtual Control Plane (comprising of kube-apiserver, etcd, kube-scheduler).
* Recommender which is trimmed down version of the proposed replacement of the current CA.


## Prerequisites

Clone the following github repositories:
1. [gardener-scaling-history](https://github.com/elankath/gardener-scaling-history)
2. [scaling-recommender](https://github.com/unmarshall/scaling-recommender)
3. [gardener-virtual-autoscaler](https://github.com/elankath/gardener-virtual-autoscaler)
4. [kvcl](https://github.com/unmarshall/kvcl)

## Local Setup

### Record cluster data
1. Ensure you are at the base dir of the [gardener-scaling-history](https://github.com/elankath/gardener-scaling-history) repo
1. Prepare `clusters.csv` for recording. This is a CSV file with 3 columns: landscape name, project name, shoot name
   An example clusters.csv file for recording data for the `g2` cluster of project `i034796` in the `dev` landscape is shown below.

    ```clusters.csv
    dev,i034796,g2
    ```
   - This file can be found in `cfg/clusters.csv` within the `gardener-scaling-history` repo
   - Update this file with records for each cluster that you wish to record data.
2. Set the following environment variables 
   1. `export MODE=local` 
   2. (Optional) `export CONFIG_DIR=<configDir>` # Dir of clusters.csv 
      - Defaults to `cfg/`. Set this only if clusters.csv is moved to a different dir 
   3. (Optional) `export DB_DIR=/tmp` # Directory where recorder stores SQLite <clusterName>.db files
      - Defaults to `gen/`. Set this only if you want DBs to be stored in a different dir
3. Execute: `go run cmd/recorder/main.go`

### Run replayer

#### Run all components together (recommended)

##### Prepare for run
1. Run `export INPUT_DATA_PATH=<Path to DB file>`. #This is the path to the recorded DB file obtained from the recorder.
   1. Ex: `export INPUT_DATA_PATH=gen/dev_i034796_g2.db`
2. (Optional) `export REPORT_DIR=<reportDir>` # Dir where you would like the replayer to store produced reports 
   1. Defaults to `/tmp`
3. Ensure you are at the base dir of the [gardener-scaling-history](https://github.com/elankath/gardener-scaling-history) repo
4. Run `./hack/build-replayer.sh local`
   1. This builds binaries of all dependencies needed by the replayer such as kube-apiserver, etcd, kube-scheduler, virtual-cluster-autoscaler
   2. Binaries are stored in `bin/`

##### Run
1. Ensure you are at the base dir of the [gardener-scaling-history](https://github.com/elankath/gardener-scaling-history) repo
2. Run `go run cmd/replayer/main.go`
   1. Reports generated are stored in `REPORT_DIR` and have the naming convention `<landscape>_<cluster>_ca-replay-<interval-num>.json`
3. (Optional) In case you wish to target the kubernetes cluster and see for yourself what is going on
   1. `export KUBECONFIG=/tmp/kvcl.yaml`

#### Run individual components separately
In case you wish to debug the replayer and hence wish to run all components separately please follow these steps

##### Run kvcl
1. Ensure you are in the base dir of [kvcl](https://github.com/unmarshall/kvcl)
2. Run `./hack/setup.sh`
3. Execute `set -o allexport && source launch.env && set +o allexport`
4. Run `go run cmd/main.go`
   - This will generate a kubeconfig at `/tmp/kvcl.yaml`

##### Run virtual cluster autoscaler
1. In a separate terminal ensure you are at the base dir of [virtual CA](https://github.com/elankath/gardener-virtual-autoscaler)
2. Move into the cluster-autoscaler dir with `cd cluster-autoscaler`
3. Build the autoscaler using `go build -o ../bin/cluster-autoscaler main.go`
4. Launch the autoscaler using `../bin/cluster-autoscaler --kubeconfig=/tmp/kvcl.yaml --v=3 | tee /tmp/cas.log`

##### Run replayer
1. In a separate terminal ensure you are at the base dir of the [gardener-scaling-history](https://github.com/elankath/gardener-scaling-history) repo
2. Run `export INPUT_DATA_PATH=<Path to DB file>`. #This is the path to the recorded DB file obtained from the recorder.
   1. Ex: `export INPUT_DATA_PATH=gen/dev_i034796_g2.db`
3. (Optional) `export REPORT_DIR=<reportDir>` # Dir where you would like the replayer to store produced reports
   1. Defaults to `/tmp`
4. Run `export NO_AUTO_LAUNCH=true`
5. Run `go run cmd/replayer/main.go`
   1. Reports generated are stored in `REPORT_DIR` and have the naming convention `<landscape>_<cluster>_ca-replay-<interval-num>.json`

### Run recommender

#### Run all components together (recommended)

##### Prepare for run
1. Run `export INPUT_DATA_PATH=<path to json report produced by replayer>` #This is the path to the report produced by the replayer
   1. (eg: `export INPUT_DATA_PATH=$GOPATH/src/github.tools.sap/I034796/gardener-scaling-reports/independent-scenarios/live_hc-eu30_prod-gc-dmi_ca-replay-5.json`)
2. (Optional) `export REPORT_DIR=<reportDir>` # Dir where you would like the replayer to store produced reports
   1. Defaults to `/tmp`
3. Ensure you are at the base dir of the [gardener-scaling-history](https://github.com/elankath/gardener-scaling-history) repo
4. Run `./hack/build-replayer.sh local`
   1. This builds binaries of all dependencies needed by the replayer such as kube-apiserver, etcd, kube-scheduler, virtual-cluster-autoscaler
   2. Binaries are stored in `bin/`

##### Run
1. Ensure you are at the base dir of the [gardener-scaling-history](https://github.com/elankath/gardener-scaling-history) repo
2. Run `go run cmd/replayer/main.go`
   1. 1. Reports generated are stored in `REPORT_DIR` and have the naming convention `<landscape>_<cluster>_sr-replay-<interval-num>.json`

#### Run individual components separately
In case you wish to debug the replayer and hence wish to run all components separately please follow these steps

##### Run kvcl
1. Ensure you are in the base dir of [kvcl](https://github.com/unmarshall/kvcl)
2. Run `./hack/setup.sh`
3. Execute `set -o allexport && source launch.env && set +o allexport`
4. Run `go run cmd/main.go`
   - This will generate a kubeconfig at `/tmp/kvcl.yaml`

##### Run recommender
1. In a separate terminal ensure you are at the base dir of [recommender](https://github.com/unmarshall/scaling-recommender)
2. Run `go run main.go --target-kvcl-kubeconfig <path-to-kubeconfig> --provider <cloud-provider> --binary-assets-path <path-to-binary-assets>`
   1. To fetch binary asset path, run `setup-envtest --os $(go env GOOS) --arch $(go env GOARCH) use $ENVTEST_K8S_VERSION -p path`

##### Run replayer
1. In a separate terminal ensure you are at the base dir of the [gardener-scaling-history](https://github.com/elankath/gardener-scaling-history) repo
2. Run `export INPUT_DATA_PATH=<path to json report produced by replayer>` #This is the path to the report produced by the replayer
   1. (eg: `export INPUT_DATA_PATH=$GOPATH/src/github.tools.sap/I034796/gardener-scaling-reports/independent-scenarios/live_hc-eu30_prod-gc-dmi_ca-replay-5.json`)
3. (Optional) `export REPORT_DIR=<reportDir>` # Dir where you would like the replayer to store produced reports
   1. Defaults to `/tmp`
4. Run `export NO_AUTO_LAUNCH=true`
5. Run `go run cmd/replayer/main.go`
   1. Reports generated are stored in `REPORT_DIR` and have the naming convention `<landscape>_<cluster>_sr-replay-<interval-num>.json`

### Run Comparator
The comparator compares a `ca-replay` report with a `sr-report` and lists out the main points of difference between the scale ups chosen. 
The report produced is in the form of a markdown file with the naming convention 

#### Run
1. Ensure you are at the base dir of the [gardener-scaling-history](https://github.com/elankath/gardener-scaling-history) repo
2. Run `go run cmd/comparer/main.go --provider=<aws|gcp> --ca-report-path=<path to ca-replay report> --sr-report-path=<path to sr-replay report> --report-out-dir=<reportDir>`
   1. eg: `go run cmd/comparer/main.go --provider=aws --ca-report-path=live_hct-us10_prod-hdl_ca-replay-10.json --sr-report-path=live_hct-us10_prod-hdl_sr-replay-10.json`
   2. `--report-out-dir` is an optional parameter. Defaults to `/tmp`
   3. `--provider` is an optional parameter. Defaults to `aws`
   
   
## Remote Setup

### Choosing a Machine Type

We recommend machines with at least `8CPU 32GB` configuration. Machines with configuration lower than this could experience degraded performance.

### Secret
The recorder needs permission to be able to access all target shoots within target landscapes. Hence a secret is needed which will allow the recorder to create the required short-lived viewer-kubeconfigs.

This secret should contain kubeconfigs of the landscapes from where shoot data is to be recorded.

> **This secret is already deployed in `mcm-ca-team` namespace of a live cluster `utility-int` in the `garden-ops` project.**
> 
> **We recommend using this cluster to try out the setup.**

### Launch Recorder

This pod is responsible for recording scaling data for predetermined clusters.

#### Prepare for launch
1. Prepare `clusters.csv` for recording. This is a CSV file with 3 columns: landscape name, project name, shoot name
   An example clusters.csv file for recording data for the `g2` cluster of project `i034796` in the `dev` landscape is shown below.

    ```clusters.csv
    dev,i034796,g2
    ```
   - This file can be found in `cfg/clusters.csv` within the `gardener-scaling-history` repo
   - Update this file with records for each cluster that you wish to record data.
2. Login into `utility-int` cluster `gardenctl target --garden sap-landscape-live --project garden-ops --shoot utility-int`
   - If you want to use a different cluster to run this setup and already have the required secret deployed there, then please log into that cluster
3. Export your docker hub username: `export DOCKERHUB_USER=<dockerHubUser>`
   - This dockerhub account is used to push images built by the subsequent steps
4. Login into Docker Hub: `docker login -u $DOCKERHUB_USER -p <dockerHubPass>`
5. Ensure you are at the base dir of the [gardener-scaling-history](https://github.com/elankath/gardener-scaling-history) repo
6. Run `./hack/build-recorder.sh`
   - This step builds a docker file for the recorder and pushed it to the dockerhub account from step 3

#### Launch
Please ensure you are at the base dir of the [gardener-scaling-history](https://github.com/elankath/gardener-scaling-history) repo
1. Run `./hack/deploy-recorder.sh`
   1. In case you are deploying this setup on your own in your own cluster, please go to `hacks/recorder.yaml` and adjust the namespace of the pod accordingly

### Launch Analyser

#### Prepare for launch
To prepare for the launch of the analyzer app kindly follow these steps

1. Login into `utility-int` cluster `gardenctl target --garden sap-landscape-live --project garden-ops --shoot utility-int`
   - If the recorder had been deployed in a different cluster, please target that particular cluster.
2. Export your docker hub username: `export DOCKERHUB_USER=<dockerHubUser>`
   - This dockerhub account is used to push images built by the subsequent steps
3. Login into Docker Hub: `docker login -u $DOCKERHUB_USER -p <dockerHubPass>`
4. Ensure you are at the base dir of the [gardener-scaling-history](https://github.com/elankath/gardener-scaling-history) repo for the next two steps
5. Run `./hack/build-replayer.sh remote` 
   - This step builds a docker file for the replayer and pushes it to the dockerhub account from step 2. 
6. Run `./hack/build-app.sh`
   - This step builds the docker file for the analyzer pod and pushes it to the dockerhub account from step 2

#### Launch
Please ensure you are at the base dir of the [gardener-scaling-history](https://github.com/elankath/gardener-scaling-history) repo
1. Run `./hack/deploy-app.sh`
   1. In case you are deploying this setup on your own in your own cluster, please go to `hacks/recorder.yaml` and adjust the namespace of the pod accordingly


The analyzer pod will periodically create two new pods
1. A replay pod with the naming convention `scaling-history-replayer-ca-<landscape>-<cluster>`
   1. This pod contains kube api-server, etcd, kube-scheduler, and a virtual autoscaler. Scenarios are created out of the recorded data, and are applied to a virtual control plane containing a virtual CA. Node scaleups done by the virtual autoscaler are recorder and written to a report called a `ca-replay` report
   2. These `ca-replay` reports have the naming convention `<landscape>_<cluster>_ca-replay-<interval-num>.json`
2. A replay pod with the naming convention `scaling-history-replayer-sr-<landscape>-<cluster>`
   1. This pod contains a kube api-server, etcd, kube-scheduler, and a recommender. Events from the `ca-replay` report are applied to the virtual control plane containing the recommender. Node scaleups recommended by the recommender and recorded and written to a report called `sr-replay` report
   2. These `sr-replay` reports have the naming convention: `<landscape>_<cluster>_sr-replay-<interval-num>.json`

The analyzer app also compares `ca-replay` reports with their corresponding `sr-replay` reports and generates a comparison report
   - This comparison report had the naming convention `<landscape>_<cluster>-<interval-num>.md`

All reports are written to the `/data/reports` dir of the volume attached to the analyzer pod


## Download DBs, reports, and logs
The analyzer pod provides a provision to download DBs, reports (which including `ca-reports`, `sr-reports`, and comparison reports) as well as log files in case you wish to. Please refer to the following.

### Download DB
#### Download all DBs
1. Run the script `./hack/download-db.sh`
#### Download a specific DB
1. Export the name of the DB you want to download `export DOWNLOAD_DB=<DB_name>`
2. Run the script `./hack/download-db.sh`

### Download scaling reports and comparison reports
#### Show list of reports available
1. Run `curl 10.47.254.238/api/reports` to display a list of all reports available

#### Download reports
1. Run the script `./hack/download-reports.sh`
   1. This will display a list of all available reports
   2. Please choose the reports you wish to download. Multiple reports can be closed using the <space_bar> key
   3. This will download replay reports into your local `/tmp` directory.

### Download logs
#### Show list of logs available
1. Run `curl 10.47.254.238/api/logs` to display a list of all reports available

#### Download a log file
1. Run `curl -kLO 10.47.254.238/api/logs/{log_file_name}` to download a log file
