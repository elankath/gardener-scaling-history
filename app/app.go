package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/dustin/go-humanize"
	gsh "github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/apputil"
	"github.com/elankath/gardener-scaling-history/specs"
	_ "github.com/mattn/go-sqlite3"
	"io"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type DefaultApp struct {
	io.Closer
	params       Params
	ctx          context.Context
	cancelFunc   context.CancelCauseFunc
	stopCh       <-chan struct{}
	mux          *http.ServeMux
	httpServer   *http.Server
	kubeclient   *kubernetes.Clientset
	numCAReplays int
}

type Params struct {
	DBDir         string
	ReportsDir    string
	DockerHubUser string
	Mode          gsh.ExecutionMode
}

func New(parentCtx context.Context, params Params) (*DefaultApp, error) {
	appCtx, appCancelCauseFunc := context.WithCancelCause(parentCtx)
	stopCh := appCtx.Done()

	restConfig, err := clientcmd.BuildConfigFromFlags("", "")
	if err != nil {
		return nil, err
	}
	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}

	app := &DefaultApp{
		params:     params,
		ctx:        appCtx,
		cancelFunc: appCancelCauseFunc,
		stopCh:     stopCh,
		mux:        http.NewServeMux(),
		kubeclient: kubeClient,
		httpServer: &http.Server{
			Addr: ":8080",
		},
	}
	app.httpServer.Handler = app.mux
	app.mux.HandleFunc("GET /api/reports", app.ListReports)
	app.mux.HandleFunc("GET /api/reports/{reportName}", app.GetReport)
	app.mux.HandleFunc("PUT /api/reports/{reportName}", app.PutReport)
	app.mux.HandleFunc("POST /api/reports/:upload", app.UploadReports)
	app.mux.HandleFunc("GET /api/db", app.ListDatabases)
	app.mux.HandleFunc("GET /api/db/{dbName}", app.GetDatabase)
	app.mux.HandleFunc("POST /api/logs/{clusterName}", app.UploadLogs)
	app.mux.HandleFunc("GET /api/logs/{clusterName}/{fileName}", app.GetLogFile)
	app.mux.HandleFunc("GET /api/logs/{clusterName}", app.ListLogFiles)
	//app.mux.HandleFunc("GET /api/reports", app.ListReports)
	return app, nil
}

func (a *DefaultApp) Start() error {
	context.AfterFunc(a.ctx, func() {
		_ = a.doClose()
	})
	a.StartCAReplayLoop()
	defer a.httpServer.Shutdown(a.ctx)
	if err := a.httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (a *DefaultApp) StartCAReplayLoop() {
	const caReplayInterval = time.Hour * 1
	go func() {
		err := a.RunCAReplays()
		if err != nil {
			slog.Error("Error in RunCAReplays", "error", err)
		}
		for {
			select {
			case <-time.After(caReplayInterval):
				err := a.RunCAReplays()
				if err != nil {
					slog.Error("Error in RunCAReplays", "error", err)
				}
			case <-a.ctx.Done():
				return
			}
		}
	}()
}

func (a *DefaultApp) RunCAReplays() error {
	dbPaths, err := ListAllDBPaths(a.params.DBDir)
	if err != nil {
		return err
	}
	slog.Info("RunCAReplays commencing.", "dbPaths", dbPaths, "numCAReplays", a.numCAReplays)
	begin := time.Now()
	//nonce: "${NONCE}"
	//DOCKERHUB_USER
	//INPUT_DATA_PATH

	for _, dbPath := range dbPaths {
		if strings.HasSuffix(dbPath, "_copy.db") {
			continue
		}
		a.numCAReplays++
		err = a.RunCAReplay(dbPath)
		if err != nil {
			slog.Warn("Error running CA replay", "dbPath", dbPath, "error", err)
		}
	}
	slog.Info("RunCAReplays completed.", "duration", time.Now().Sub(begin), "dbPaths", dbPaths)
	return nil
}

func (a *DefaultApp) RunCAReplay(dbPath string) error {
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Hour)
	defer cancel()
	replayerYaml, err := GetReplayerPodYaml(dbPath, a.params.DockerHubUser, time.Now())
	if err != nil {
		return err
	}
	stringReader := strings.NewReader(replayerYaml)
	//yamlReader := yaml.NewYAMLReader(bufio.NewReader(stringReader))
	yamlDecoder := yaml.NewYAMLOrJSONDecoder(io.NopCloser(stringReader), 4096)
	var pod = &corev1.Pod{}
	if err = yamlDecoder.Decode(pod); err != nil {
		return err
	}
	slog.Info("RunCAReplay is deploying pod.", "podName", pod.Name, "INPUT_DATA_PATH", dbPath, "numCAReplays", a.numCAReplays)
	_, err = a.kubeclient.CoreV1().Pods(pod.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		slog.Error("RunCAReplay cannot deploy replayer pod", "podName", pod.Name, "INPUT_DATA_PATH", dbPath, "numCAReplays", a.numCAReplays, "error", err)
		return err
	}

	err = wait.PollUntilContextTimeout(ctx, 5*time.Minute, 2*time.Hour, false, func(ctx context.Context) (done bool, err error) {
		pod, err = a.kubeclient.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
		if err != nil {
			slog.Warn("RunCAReplay cannot get Pod", "podName", pod.Name, "numCAReplays", a.numCAReplays, "error", err)
			return false, err
		}

		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			slog.Info("RunCAReplay is completed", "podName", pod.Name, "INPUT_DATA_PATH", dbPath, "podPhase", pod.Status.Phase)
			return true, nil
		}
		slog.Info("RunCAReplay is in progress.", "podName", pod.Name, "INPUT_DATA_PATH", dbPath, "podPhase", pod.Status.Phase)
		return false, nil
	})
	if err != nil {
		return err
	}

	return nil
}

func GetShootAdminKubeclient(ctx context.Context, mode gsh.ExecutionMode) (*kubernetes.Clientset, error) {
	landscapeKubeConfigs, err := apputil.GetLandscapeKubeconfigs(mode)
	if err != nil {
		return nil, err
	}
	landscapeKubeconfig, ok := landscapeKubeConfigs["live"]
	if !ok {
		err = fmt.Errorf("cannot find kubeconfig for landscape %q", "live")
		return nil, err
	}
	landscapeClient, err := apputil.CreateLandscapeClient(landscapeKubeconfig, mode)
	if err != nil {
		err = fmt.Errorf("cannot create landscape client for landscape %q, projectName %q, shootName %q: %w", "live", "garden-ops", "utility-int", err)
		return nil, err
	}
	shootKubeconfigPath, err := apputil.GetAdminKubeconfig(ctx, landscapeClient, "live", "garden-ops", "utility-int")
	if err != nil {
		err = fmt.Errorf("cannot get viewer kubeconfig for shoot %q: %w", "utility-int", err)
		return nil, err
	}

	config, err := clientcmd.BuildConfigFromFlags("", shootKubeconfigPath)
	if err != nil {
		slog.Error("Error building rest config", err)
		return nil, err
	}
	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		slog.Error("Error building kubernetes clientset", err)
		return nil, err
	}

	return kubeClient, nil
}

func GetReplayerPodYaml(inputDataPath, dockerHubUser string, now time.Time) (string, error) {
	replayerPodTemplate, err := specs.GetReplayerPodYamlTemplate()
	if err != nil {
		return "", err
	}
	var memory, podName, dbPath string
	if strings.HasSuffix(inputDataPath, ".db") {
		memory = "8Gi"
	} else if strings.HasSuffix(inputDataPath, ".json") {
		memory = "16Gi"
	} else {
		return "", fmt.Errorf("invalid inputDataPath: %q", inputDataPath)
	}

	shootName := inputDataPath[:strings.LastIndex(inputDataPath, ".")]
	shootName = inputDataPath[strings.LastIndex(inputDataPath, "_")+1:]
	podName = "scaling-history-replayer-ca-" + shootName

	dbPath = "/db/" + path.Base(inputDataPath)
	//POD_DATA_PATH = "/db/${DB_NAME}"

	oldNew := []string{"${NONCE}", now.Format(time.RFC822Z), "${DOCKERHUB_USER}", dockerHubUser, "${INPUT_DATA_PATH}", inputDataPath, "${MEMORY}", memory, "${POD_NAME}", podName, "${NO_AUTO_LAUNCH}", "false", "${POD_DATA_PATH}", dbPath}
	replacer := strings.NewReplacer(oldNew...)
	return replacer.Replace(replayerPodTemplate), nil
}

func ListAllDBPaths(dir string) (dbPaths []string, err error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".db") {
			dbPaths = append(dbPaths, filepath.Join(dir, f.Name()))
		}
	}
	return
}

func (a *DefaultApp) ListReports(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	entries, err := os.ReadDir(a.params.ReportsDir)
	if err != nil {
		slog.Error("ListReports could not list files in ReportsDir", "error", err, "reportsDir", a.params.ReportsDir)
		http.Error(w, fmt.Sprintf("could not list files in reportsDir %q", a.params.ReportsDir), 500)
		return
	}
	reportInfos := []gsh.FileInfo{}
	for _, e := range entries {
		dbName := e.Name()
		if !strings.HasSuffix(dbName, ".json") {
			continue
		}
		//if strings.HasSuffix(dbName, "_copy.db") {
		//	continue
		//}
		name := e.Name()
		reportPath := filepath.Join(a.params.ReportsDir, name)
		statInfo, err := os.Stat(reportPath)
		if err != nil {
			slog.Error("ListReports could not stat report file.", "error", err, "reportPath", reportPath)
			http.Error(w, fmt.Sprintf("ListReports could not stat report file %q in reportDir %q", reportPath, a.params.ReportsDir), 500)
			return
		}
		reportSize := uint64(statInfo.Size())
		reportInfos = append(reportInfos, gsh.FileInfo{
			Name:         name,
			Size:         reportSize,
			ReadableSize: humanize.Bytes(reportSize),
		})
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", " ")
	err = encoder.Encode(gsh.FileInfos{reportInfos})
	//err = json.NewEncoder(w).Encode(gsh.FileInfos{reportInfos})
	if err != nil {
		slog.Error("ListReports could not serialize items.", "error", err, "reportInfos", reportInfos)
		http.Error(w, fmt.Sprintf("ListReports could not serialize response due to: %s", err), 500)
		return
	}
	return
}

func (a *DefaultApp) Close() error {
	a.cancelFunc(errors.New("app closed"))
	return nil
}

func (a *DefaultApp) doClose() error {
	// Add close stuff here.
	return nil
}

func (a *DefaultApp) GetReport(w http.ResponseWriter, r *http.Request) {
	reportName := r.PathValue("reportName")
	if len(reportName) == 0 {
		http.Error(w, "reportName must not be empty", 400)
		return
	}
	reportFile := path.Join(a.params.ReportsDir, reportName)
	if !apputil.FileExists(reportFile) {
		http.Error(w, fmt.Sprintf("reportFile %q not found", reportFile), 400)
		return
	}
	slog.Info("Serving report.", "reportFile", reportFile)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+reportName+"\"")
	http.ServeFile(w, r, reportFile)
}

func (a *DefaultApp) PutReport(w http.ResponseWriter, r *http.Request) {
	reportName := r.PathValue("reportName")
	if len(reportName) == 0 {
		http.Error(w, "reportName must not be empty", 400)
		return
	}
	reportPath := path.Join(a.params.ReportsDir, reportName)

	slog.Info("Coping request body to report path", "reportPath", reportPath)

	data, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("PutReport could not read request body", "error", err, "reportPath", reportPath)
		http.Error(w, fmt.Sprintf("PutReport could not read request body: %s", err), 500)
		return
	}

	err = os.WriteFile(reportPath, data, 0644)
	if err != nil {
		slog.Error("PutReport could not write request body", "error", err, "reportPath", reportPath)
		http.Error(w, fmt.Sprintf("PutReport could not write request body to report path %q: %s", reportPath, err), 500)
		return
	}
}

func (a *DefaultApp) UploadReports(w http.ResponseWriter, r *http.Request) {
	var err error
	err = r.ParseMultipartForm(100 << 20)
	if err != nil {
		slog.Error("UploadReports could not parse multi-part form", "error", err)
		http.Error(w, "UploadReports could not parse multi-part form:"+err.Error(), 500)
		return
	}

	multipartFormData := r.MultipartForm
	for _, filePart := range multipartFormData.File["reports"] {
		slog.Info("Accepting upload report.", "file", filePart.Filename, "size", filePart.Size)
		data, ok := ReadFilePart(w, filePart)
		if !ok {
			return
		}
		reportPath := filepath.Join(a.params.ReportsDir, filePart.Filename)
		err = os.WriteFile(reportPath, data, 0600)
		if err != nil {
			slog.Error("UploadReports could not write report", "reportPath", reportPath, "error", err)
			http.Error(w, "UploadReports could not write report: "+reportPath, 500)
			return
		}
		slog.Info("Uploaded report", "reportPath", reportPath)
	}
}

func (a *DefaultApp) GetDatabase(w http.ResponseWriter, r *http.Request) {
	dbName := r.PathValue("dbName")
	if len(dbName) == 0 {
		http.Error(w, "dbName must not be empty", 400)
		return
	}
	dbFile := path.Join(a.params.DBDir, dbName)
	if !apputil.FileExists(dbFile) {
		http.Error(w, fmt.Sprintf("dbFile %q not found", dbFile), 400)
		return
	}
	ext := filepath.Ext(dbFile)
	dbCopyFile := dbFile[:len(dbFile)-len(ext)] + "_copy" + ext
	err := apputil.CopySQLiteDB(dbFile, dbCopyFile)
	if err != nil {
		http.Error(w, fmt.Sprintf("could not make copy SQLite db file to serve %q: %v", dbFile, err), 500)
		return
	}
	slog.Info("Serving copy of DB.", "dbFile", dbCopyFile)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(dbCopyFile)+"\"")
	http.ServeFile(w, r, dbCopyFile)
}

func (a *DefaultApp) ListDatabases(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	entries, err := os.ReadDir(a.params.DBDir)
	if err != nil {
		slog.Error("ListDatabases could not list files in dbDir", "error", err, "dbDir", a.params.DBDir)
		http.Error(w, fmt.Sprintf("could not list files in dbDir %q", a.params.DBDir), 500)
		return
	}
	dbInfos := []gsh.FileInfo{}
	for _, e := range entries {
		dbName := e.Name()
		if !strings.HasSuffix(dbName, ".db") {
			continue
		}
		if strings.HasSuffix(dbName, "_copy.db") {
			continue
		}
		name := e.Name()
		dbPath := filepath.Join(a.params.DBDir, name)
		statInfo, err := os.Stat(dbPath)
		if err != nil {
			slog.Error("ListDatabases could not stat db file.", "error", err, "dbPath", dbPath)
			http.Error(w, fmt.Sprintf("ListDatabases could not stat db file %q in dbDir %q", dbPath, a.params.DBDir), 500)
			return
		}
		dbSize := uint64(statInfo.Size())
		dbInfos = append(dbInfos, gsh.FileInfo{
			Name:         name,
			Size:         dbSize,
			ReadableSize: humanize.Bytes(dbSize),
		})
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", " ")
	//err = json.NewEncoder(w).Encode(gsh.FileInfos{dbInfos})
	err = encoder.Encode(gsh.FileInfos{dbInfos})
	if err != nil {
		slog.Error("ListDatabases could not serialize items.", "error", err, "dbInfos", dbInfos)
		http.Error(w, fmt.Sprintf("ListDatabases could not serialize response due to: %s", err), 500)
		return
	}
	return
}

func (a *DefaultApp) UploadLogs(w http.ResponseWriter, r *http.Request) {
	var err error
	clusterName := strings.TrimSpace(r.PathValue("clusterName"))
	if len(clusterName) == 0 {
		http.Error(w, "clusterName must not be empty", 400)
		return
	}
	clusterLogsDir := "/data/logs/" + clusterName
	err = os.MkdirAll(clusterLogsDir, 0777)
	if err != nil {
		http.Error(w, fmt.Sprintf("could not create logs directory %q: %v", clusterLogsDir, err), 500)
		return
	}
	err = r.ParseMultipartForm(100 << 20)
	if err != nil {
		slog.Error("UploadLogs could not parse multi-part form", "error", err)
		http.Error(w, "UploadLogs could not parse multi-part form:"+err.Error(), 500)
		return
	}

	logFileInfos := []gsh.FileInfo{}
	multipartFormData := r.MultipartForm
	for _, filePart := range multipartFormData.File["logs"] {
		slog.Info("Accepting upload log.", "file", filePart.Filename, "size", filePart.Size)
		data, ok := ReadFilePart(w, filePart)
		if !ok {
			return
		}
		logFilePath := filepath.Join(clusterLogsDir, filePart.Filename)
		err = os.WriteFile(logFilePath, data, 0600)
		if err != nil {
			slog.Error("UploadLogs could not write log file", "logFilePath", logFilePath, "error", err)
			http.Error(w, "UploadLogs could not write log file: "+logFilePath, 500)
			return
		}
		slog.Info("Uploaded log file", "logFilePath", logFilePath)
		logFileSz := uint64(filePart.Size)
		logFileInfos = append(logFileInfos, gsh.FileInfo{
			Name:         logFilePath,
			Size:         logFileSz,
			ReadableSize: humanize.Bytes(logFileSz),
		})
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", " ")
	err = encoder.Encode(gsh.FileInfos{logFileInfos})
	if err != nil {
		slog.Error("UploadLogs could not serialize items.", "error", err, "logFileInfos", logFileInfos)
		http.Error(w, fmt.Sprintf("UploadLogs could not serialize response due to: %s", err), 500)
		return
	}
}

func (a *DefaultApp) GetLogFile(w http.ResponseWriter, r *http.Request) {
	clusterName := strings.TrimSpace(r.PathValue("clusterName"))
	if len(clusterName) == 0 {
		http.Error(w, "clusterName must not be empty", 400)
		return
	}
	clusterLogsDir := "/data/logs/" + clusterName

	logFileName := r.PathValue("fileName")
	if len(logFileName) == 0 {
		http.Error(w, "logFileName must not be empty", 400)
		return
	}
	logFilePath := path.Join(clusterLogsDir, logFileName)
	if !apputil.FileExists(logFilePath) {
		http.Error(w, fmt.Sprintf("logFilePath %q not found", logFilePath), 400)
		return
	}
	slog.Info("Serving log file.", "logFilePath", logFilePath)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+logFileName+"\"")
	http.ServeFile(w, r, logFilePath)
}

func (a *DefaultApp) deployDummyPod() error {
	var pod corev1.Pod
	podYaml, err := specs.GetPodYaml("smallPod.yaml")
	if err != nil {
		return err
	}
	stringReader := strings.NewReader(podYaml)
	//yamlReader := yaml.NewYAMLReader(bufio.NewReader(stringReader))
	yamlDecoder := yaml.NewYAMLOrJSONDecoder(io.NopCloser(stringReader), 4096)
	if err := yamlDecoder.Decode(&pod); err != nil {
		return err
	}
	pod.Namespace = "mcm-ca-team"
	slog.Info("Deploying dummy pod: ", "name", pod.Name)
	_, err = a.kubeclient.CoreV1().Pods(pod.Namespace).Create(a.ctx, &pod, metav1.CreateOptions{})
	if err != nil {
		slog.Error("Error deploying dummy pod", "name", pod.Name)
		return err
	}
	return nil
}

func (a *DefaultApp) ListLogFiles(w http.ResponseWriter, r *http.Request) {
	var err error
	clusterName := strings.TrimSpace(r.PathValue("clusterName"))
	if len(clusterName) == 0 {
		http.Error(w, "clusterName must not be empty", 400)
		return
	}
	clusterLogsDir := "/data/logs/" + clusterName

	w.Header().Set("Content-Type", "application/json")
	entries, err := os.ReadDir(clusterLogsDir)
	if err != nil {
		slog.Error("ListLogFiles could not list files in clusterLogsDir", "error", err, "clusterLogsDir", clusterLogsDir)
		http.Error(w, fmt.Sprintf("could not list files in clusterLogsDir %q", clusterLogsDir), 500)
		return
	}
	logFileInfos := []gsh.FileInfo{}
	for _, e := range entries {
		logFileName := e.Name()
		if !strings.HasSuffix(logFileName, ".log") {
			continue
		}
		logFilePath := filepath.Join(clusterLogsDir, logFileName)
		statInfo, err := os.Stat(logFilePath)
		if err != nil {
			slog.Error("ListLogFiles could not stat log file.", "error", err, "logFilePath", logFilePath)
			http.Error(w, fmt.Sprintf("ListLogFiles could not stat log file %q in clusterLogsDir %q", logFilePath, clusterLogsDir), 500)
			return
		}
		logFileSize := uint64(statInfo.Size())
		logFileInfos = append(logFileInfos, gsh.FileInfo{
			Name:         logFileName,
			Size:         logFileSize,
			ReadableSize: humanize.Bytes(logFileSize),
		})
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", " ")
	err = encoder.Encode(gsh.FileInfos{logFileInfos})
	//err = json.NewEncoder(w).Encode(gsh.FileInfos{logFileInfos})
	if err != nil {
		slog.Error("ListLogFiles could not serialize items.", "error", err, "logFileInfos", logFileInfos)
		http.Error(w, fmt.Sprintf("ListLogFiles could not serialize response due to: %s", err), 500)
		return
	}
	return
}

func ReadFilePart(w http.ResponseWriter, filePart *multipart.FileHeader) (data []byte, ok bool) {
	var err error
	uploadedFile, err := filePart.Open()
	if err != nil {
		slog.Error("UploadReports could not open report file", "file", filePart.Filename, "error", err)
		http.Error(w, "UploadReports could not open report file: "+err.Error(), 500)
		return
	}
	// then use the single uploadedFile however you want
	// you may use its read method to get the file's bytes into a predefined slice,
	//here am just using an anonymous slice for the example
	data, err = io.ReadAll(uploadedFile)
	if err != nil {
		slog.Error("UploadReports could not read report file", "file", filePart.Filename, "error", err)
		http.Error(w, "UploadReports could not read report file: "+err.Error(), 500)
		return
	}
	err = uploadedFile.Close()
	if err != nil {
		slog.Error("UploadReports could not close report file", "file", filePart.Filename)
		http.Error(w, "UploadReports could not close report file: "+err.Error(), 500)
		return
	}
	ok = true
	return
}
