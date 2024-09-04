package recorder

import (
	"context"
	"fmt"
	"github.com/elankath/gardener-scaling-history/apputil"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
)

type FileServer struct {
	serverCtx  context.Context
	httpServer *http.Server
	mux        *http.ServeMux
	dbDir      string
	reportDir  string
}

func LaunchFileServer(ctx context.Context, dbDir string, reportDir string) error {
	mux := http.NewServeMux()

	httpServer := http.Server{
		Addr: ":8080",
	}

	fileServer := &FileServer{
		serverCtx:  ctx,
		httpServer: &httpServer,
		mux:        mux,
		dbDir:      dbDir,
		reportDir:  reportDir,
	}

	fileServer.mux.HandleFunc("GET /db", fileServer.ListDatabases)
	fileServer.mux.HandleFunc("GET /db/{dbName}", fileServer.GetDatabase)
	fileServer.mux.HandleFunc("GET /reports", fileServer.ListReports)
	fileServer.mux.HandleFunc("GET /reports/{reportName}", fileServer.GetReport)

	fileServer.httpServer.Handler = fileServer.mux
	defer fileServer.httpServer.Shutdown(ctx)
	if err := fileServer.httpServer.ListenAndServe(); err != nil {
		return err
	}

	return nil
}

func (f *FileServer) ListDatabases(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	entries, err := os.ReadDir(f.dbDir)
	if err != nil {
		slog.Error("ListDatabases could not list files in dbDir", "error", err, "dbDir", f.dbDir)
		http.Error(w, fmt.Sprintf("could not list files in dbDir %q", f.dbDir), 500)
		return
	}
	for _, e := range entries {
		dbName := e.Name()
		if !strings.HasSuffix(dbName, ".db") {
			continue
		}
		_, _ = fmt.Fprintln(w, dbName)
	}
}

func (f *FileServer) GetDatabase(w http.ResponseWriter, r *http.Request) {
	dbName := r.PathValue("dbName")
	if len(dbName) == 0 {
		http.Error(w, "dbName must not be empty", 400)
		return
	}
	dbFile := path.Join(f.dbDir, dbName)
	if !apputil.FileExists(dbFile) {
		http.Error(w, fmt.Sprintf("dbFile %q not found", dbFile), 400)
		return
	}
	slog.Info("Serving DB.", "dbFile", dbFile)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+dbName+"\"")
	http.ServeFile(w, r, dbFile)
}

func (f *FileServer) ListReports(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	entries, err := os.ReadDir(f.reportDir)
	if err != nil {
		slog.Error("ListReports could not list files in reportDir", "error", err, "reportDir", f.reportDir)
		http.Error(w, fmt.Sprintf("could not list files in reportDir %q", f.reportDir), 500)
		return
	}
	for _, e := range entries {
		entryName := e.Name()
		if !strings.HasSuffix(entryName, ".json") {
			continue
		}
		_, _ = fmt.Fprintln(w, entryName)
	}
}
func (f *FileServer) GetReport(w http.ResponseWriter, r *http.Request) {
	reportName := r.PathValue("reportName")
	if len(reportName) == 0 {
		http.Error(w, "reportName must not be empty", 400)
		return
	}
	reportFile := path.Join(f.reportDir, reportName)
	if !apputil.FileExists(reportFile) {
		http.Error(w, fmt.Sprintf("reportFile %q not found", reportFile), 400)
		return
	}
	slog.Info("Serving DB.", "reportFile", reportFile)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+reportName+"\"")
	http.ServeFile(w, r, reportFile)
}
