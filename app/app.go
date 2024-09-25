package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/dustin/go-humanize"
	"github.com/elankath/gardener-scaling-history/specs"
	"io"
	"k8s.io/apimachinery/pkg/util/json"
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
}

type Params struct {
	DBDir         string
	ReportsDir    string
	DockerHubUser string
}

func New(parentCtx context.Context, params Params) *DefaultApp {
	appCtx, appCancelCauseFunc := context.WithCancelCause(parentCtx)
	stopCh := appCtx.Done()

	app := &DefaultApp{
		params:     params,
		ctx:        appCtx,
		cancelFunc: appCancelCauseFunc,
		stopCh:     stopCh,
		mux:        http.NewServeMux(),
		httpServer: &http.Server{
			Addr: ":8080",
		},
	}
	app.httpServer.Handler = app.mux
	app.mux.HandleFunc("GET /api/db", app.ListDatabases)
	//app.mux.HandleFunc("GET /api/db/{dbName}", app.GetDatabase)
	//app.mux.HandleFunc("GET /api/reports", app.ListReports)
	//app.mux.HandleFunc("GET /api/reports/{reportName}", app.GetReport)
	return app
}

func (a *DefaultApp) Start() error {
	context.AfterFunc(a.ctx, func() {
		_ = a.doClose()
	})
	defer a.httpServer.Shutdown(a.ctx)

	if err := a.httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

//func (a *DefaultApp) StartCAReplays() error {
//	_dbPaths, err := ListAllDBPaths(a.params.DBDir)
//	if err != nil {
//		return err
//	}
//	//nonce: "${NONCE}"
//	//DOCKERHUB_USER
//	//INPUT_DATA_PATH
//
//	//for _, dbPath := range dbPaths {
//	//	oldNew := []string{"${NONCE}", time.Now().String(), "${DOCKERHUB_USER}", a.params.DockerHubUser, ""}
//	//}
//}

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
