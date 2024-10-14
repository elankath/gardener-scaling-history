> This document is meant to be a guide on how to start the whole setup this whole POC


## Requirements

### Code Repositories
The following repositories are needed
1. [gardener-scaling-history](https://github.com/elankath/gardener-scaling-history)
2. [scaling-recommender](https://github.com/unmarshall/scaling-recommender)
3. [gardener-virtual-autoscaler](https://github.com/elankath/gardener-virtual-autoscaler)
4. [kvcl](https://github.com/unmarshall/kvcl)

### Machine types
Ideally, we recommend machines with at least `8CPU 32GB` configuration. Machines with configuration lower then this could experience degraded performance.

### Secret
The recorder needs permission to be able to access all shoots within all landscapes. Hence a secret is needed which will allow the recorder to create the required kubeconfigs.

> We have already configures a live cluster `utility-int` in the `garden-ops` project with these machines.
> The required secret is also available within this cluster in the `mcm-ca-team` namespace 
> You can use this namespace to try your setup, This will eliminate the hassle of getting the cluster ready 

## What to launch
You would need to launch 2 pods
### 1. Recorder
This pod is responsible for recording scaling data for predetermined clusters.

#### Launch
1. Prepare `clusters.csv` for recording. This is a CSV file with 3 columns: landscape name, project name, shoot name
   An example clusters.csv file for recording data for the `g2` cluster of project `i034796` in the `dev` landscape is shown below.
    ```clusters.csv
    dev,i034796,g2
    ```
   1. This file can be found in `cfg/clusters.csv`
2. Login into `utility-int` cluster `gardenctl target --garden sap-landscape-live --project garden-ops --shoot utility-int`
3. Export your docker hub username: `export DOCKERHUB_USER=<dockerHubUser>`
4. Login into Docker Hub: `docker login -u $DOCKERHUB_USER -p <dockerHubPass>`
5. Run `./hack/build-recorder.sh`
6. Run `./hack/deploy-recorder.sh `

### 2. scaling-history-app pod
The scaling-history-app does the following
- Periodically creates virtual CA pods for each cluster DB being recorded. These pods generate reports with scaling actions taken by the virtual CA
- Periodically creates recommender pods for each virtual CA report generated. These pods generate reports with scaling actions recommended by the recommender
- Compares virtual CA report with their corresponding recommender report and generates a comparison report
 
#### Launch
To launch the scaling-history-app pod follow these steps
1. Login into `utility-int` cluster `gardenctl target --garden sap-landscape-live --project garden-ops --shoot utility-int`
2. Export your docker hub username: `export DOCKERHUB_USER=<dockerHubUser>`
3. Login into Docker Hub: `docker login -u $DOCKERHUB_USER -p <dockerHubPass>`
4. Ensure you are at the base dir of the gardener-scaling-history
5. Run `./hack/build-replayer.sh remote` (This step builds and pushed to docker the binary that is used in the virtual CA and recommender pods that will be created)
6. Run `./hack/build-app.sh`
7. Run `./hack/deploy-app.sh`

This will periodically deploy a `a` pod that will run the replayer and generate the report into `/data/reports` dir on the volume attached to the pod
This will periodically deploy a `a` pod that will run the recommender and generate the report into `/data/reports` dir on the volume attached to the pod

## Download DBs, reports, and logs
The `scaling-history-app` pod provides a provision to download DBs, all reports including VCA reports, SR reports, comparison reports, as well as log files in case you wish to. Please refer to the following.

### Download DB
#### Download all DBs
1. Kindly run the script `./hack/download-db.sh`
#### Download a specific DB
1. Export the name of the DB you want to download `export DOWNLOAD_DB=<DB_name>`
2. Kindly run the script `./hack/download-db.sh`

### Download scaling reports and comparison reports
#### Show list of reports available
1. Kindly run `curl 10.47.254.238/api/reports` to display a list of all reports available

#### Download reports
1. Kindly run the script `./hack/download-reports.sh`
   1. This will display a list of all available reports
   2. Please choose the reports you wish to download. Multiple reports can be closed using the <space_bar> key
   3. This will download replay reports into your local `/tmp` directory.

### Download logs
#### Show list of logs available
1. Kindly run `curl 10.47.254.238/api/logs` to display a list of all reports available

#### Download a log file
1. Kindly run `curl -kLO 10.47.254.238/api/logs/{log_file_name}` to download a log file

