package desktopapp

import (
	"context"
	"fmt"
	"github.com/dustin/go-humanize"
	gsh "github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/apputil"
	"io"
	"k8s.io/apimachinery/pkg/util/json"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type DefaultDesktopApp struct {
	io.Closer
	params     Params
	ctx        context.Context
	cancelFunc context.CancelCauseFunc
	stopCh     <-chan struct{}
	mux        *http.ServeMux
	httpServer *http.Server
}

type Params struct {
	ReportsDir   string
	CloudAppHost string
}

func New(parentCtx context.Context, params Params) (*DefaultDesktopApp, error) {
	appCtx, appCancelCauseFunc := context.WithCancelCause(parentCtx)
	stopCh := appCtx.Done()

	app := &DefaultDesktopApp{
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
	app.mux.HandleFunc("GET /api/reports", app.ListReports)
	app.mux.HandleFunc("GET /api/reports/{reportName}", app.GetReport)
	//app.mux.HandleFunc("PUT /api/reports/{reportName}", app.PutReport)
	//app.mux.HandleFunc("POST /api/reports/:upload", app.UploadReports)
	//app.mux.HandleFunc("GET /api/db", app.ListDatabases)
	//app.mux.HandleFunc("GET /api/db/{dbName}", app.GetDatabase)
	//app.mux.HandleFunc("POST /api/logs/{clusterName}", app.UploadLogs)
	//app.mux.HandleFunc("GET /api/logs/{clusterName}/{fileName}", app.GetLogFile)
	//app.mux.HandleFunc("GET /api/logs/{clusterName}", app.ListLogFiles)
	//app.mux.HandleFunc("GET /api/reports", app.ListReports)
	return app, nil
}
func (a *DefaultDesktopApp) ListReports(w http.ResponseWriter, r *http.Request) {
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

func (a *DefaultDesktopApp) GetReport(w http.ResponseWriter, r *http.Request) {
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
