package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/dustin/go-humanize"
	gsh "github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/apputil"
	"github.com/elankath/gardener-scaling-history/specs"
	"io"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"log/slog"
	"net/http"
	"os"
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

	kubeClient, err := GetShootAdminKubeclient(appCtx, params.Mode)
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
	app.mux.HandleFunc("GET /api/db", app.ListDatabases)
	//app.mux.HandleFunc("GET /api/db/{dbName}", app.GetDatabase)
	//app.mux.HandleFunc("GET /api/reports", app.ListReports)
	//app.mux.HandleFunc("GET /api/reports/{reportName}", app.GetReport)
	return app, nil
}

func (a *DefaultApp) Start() error {
	context.AfterFunc(a.ctx, func() {
		_ = a.doClose()
	})
	go func() {
		err := a.RunCAReplays()
		if err != nil {
			slog.Error("Error running CA replay", err)
		}
	}()
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

type fileInfos struct {
	Items []fileInfo
}

type fileInfo struct {
	Name         string
	Size         uint64
	ReadableSize string
}

func (a *DefaultApp) ListDatabases(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	entries, err := os.ReadDir(a.params.DBDir)
	if err != nil {
		slog.Error("ListDatabases could not list files in dbDir", "error", err, "dbDir", a.params.DBDir)
		http.Error(w, fmt.Sprintf("could not list files in dbDir %q", a.params.DBDir), 500)
		return
	}
	var dbInfos []fileInfo
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
		dbInfos = append(dbInfos, fileInfo{
			Name:         name,
			Size:         dbSize,
			ReadableSize: humanize.Bytes(dbSize),
		})
	}
	err = json.NewEncoder(w).Encode(fileInfos{dbInfos})
	if err != nil {
		slog.Error("ListDatabases could not serialize items.", "error", err, "dbInfos", dbInfos)
		http.Error(w, fmt.Sprintf("ListDatabases could not serialize response due to: %s", err), 500)
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
