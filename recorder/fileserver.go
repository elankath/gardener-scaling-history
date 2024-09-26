package recorder

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/elankath/gardener-scaling-history/apputil"
	_ "github.com/mattn/go-sqlite3"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
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

	fileServer.mux.HandleFunc("GET /api/db", fileServer.ListDatabases)
	fileServer.mux.HandleFunc("GET /api/db/{dbName}", fileServer.GetDatabase)
	fileServer.mux.HandleFunc("GET /api/reports", fileServer.ListReports)
	fileServer.mux.HandleFunc("GET /api/reports/{reportName}", fileServer.GetReport)

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
		slog.Error("ListReports could not list files in dbDir", "error", err, "dbDir", f.dbDir)
		http.Error(w, fmt.Sprintf("could not list files in dbDir %q", f.dbDir), 500)
		return
	}
	for _, e := range entries {
		dbName := e.Name()
		if !strings.HasSuffix(dbName, ".db") {
			continue
		}
		if strings.HasSuffix(dbName, "_copy.db") {
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
	ext := filepath.Ext(dbFile)
	dbCopyFile := dbFile[:len(dbFile)-len(ext)] + "_copy" + ext
	err := copySQLiteDB(dbFile, dbCopyFile)
	if err != nil {
		http.Error(w, fmt.Sprintf("could not make copy SQLite db file to serve %q: %v", dbFile, err), 500)
		return
	}
	slog.Info("Serving copy of DB.", "dbFile", dbCopyFile)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(dbCopyFile)+"\"")
	http.ServeFile(w, r, dbCopyFile)
}

func copySQLiteDB(srcDBPath, dstDBPath string) error {
	//Ensure that destDB file does not exist
	err := os.Remove(dstDBPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	// Open source database connection
	db, err := sql.Open("sqlite3", srcDBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	// Execute the VACUUM INTO command to copy the database safely
	query := fmt.Sprintf("VACUUM INTO '%s';", dstDBPath)
	_, err = db.Exec(query)
	if err != nil {
		return err
	}
	return nil
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
