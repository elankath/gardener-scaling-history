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
	params     Params
	ctx        context.Context
	cancelFunc context.CancelCauseFunc
	stopCh     <-chan struct{}
	mux        *http.ServeMux
	httpServer *http.Server
	kubeclient *kubernetes.Clientset
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

	//kubeClient, err := GetShootAdminKubeclient(appCtx, params.Mode)
	//if err != nil {
	//	return nil, err
	//}

	app := &DefaultApp{
		params:     params,
		ctx:        appCtx,
		cancelFunc: appCancelCauseFunc,
		stopCh:     stopCh,
		mux:        http.NewServeMux(),
		//kubeclient: kubeClient,
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
	//app.mux.HandleFunc("GET /api/reports", app.ListReports)
	return app, nil
}

func (a *DefaultApp) Start() error {
	context.AfterFunc(a.ctx, func() {
		_ = a.doClose()
	})
	//go func() {
	//	err := a.RunCAReplays()
	//	if err != nil {
	//		slog.Error("Error running CA replay", err)
	//	}
	//}()
	defer a.httpServer.Shutdown(a.ctx)
	if err := a.httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (a *DefaultApp) RunCAReplays() error {
	dbPaths, err := ListAllDBPaths(a.params.DBDir)
	if err != nil {
		return err
	}
	//nonce: "${NONCE}"
	//DOCKERHUB_USER
	//INPUT_DATA_PATH

	for _, dbPath := range dbPaths {
		replayerYaml, err := GetReplayerPodYaml(dbPath, a.params.DockerHubUser, time.Now())
		if err != nil {
			return err
		}
		stringReader := strings.NewReader(replayerYaml)
		//yamlReader := yaml.NewYAMLReader(bufio.NewReader(stringReader))
		yamlDecoder := yaml.NewYAMLOrJSONDecoder(io.NopCloser(stringReader), 4096)
		var pod corev1.Pod
		if err := yamlDecoder.Decode(&pod); err != nil {
			return err
		}
		slog.Info("Deploying replayer pod: ", "name", pod.Name, "inputDataPath", dbPath)
		_, err = a.kubeclient.CoreV1().Pods(pod.Namespace).Create(a.ctx, &pod, metav1.CreateOptions{})
		if err != nil {
			slog.Error("Error deploying replayer pod", "name", pod.Name, "inputDataPath", dbPath)
			return err
		}
		break
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
	oldNew := []string{"${NONCE}", now.Format(time.RFC822Z), "${DOCKERHUB_USER}", dockerHubUser, "${INPUT_DATA_PATH}", inputDataPath}
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
