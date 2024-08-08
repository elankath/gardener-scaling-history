package server

import (
	"context"
	"encoding/csv"
	"encoding/json"
	gsh "github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/recorder"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

const CLUSTERS_CFG_FILE = "clusters.csv"

type engine struct {
	mux        *http.ServeMux
	httpServer *http.Server
	ctx        context.Context
}

type kubeconfigs struct {
	Shoot string `json:"shoot"`
	Seed  string `json:"seed"`
}

func Launch(ctx context.Context) error {
	mux := http.NewServeMux()

	httpServer := http.Server{
		Addr: ":8080",
	}

	engine := &engine{
		mux:        mux,
		httpServer: &httpServer,
		ctx:        ctx,
	}

	engine.mux.HandleFunc("POST /override-kubeconfigs/{shootNamespace}", engine.RestartRecorder)
	engine.mux.HandleFunc("GET /cluster-info", engine.GetClusterInfo)

	engine.httpServer.Handler = engine.mux
	defer engine.httpServer.Shutdown(ctx)
	if err := engine.httpServer.ListenAndServe(); err != nil {
		return err
	}

	return nil
}

// TODO: read /gen/clusters.csv and write it to ResponseWriter
func (e *engine) GetClusterInfo(w http.ResponseWriter, r *http.Request) {
	configDir := os.Getenv("CONFIG_DIR")
	if len(configDir) == 0 {
		slog.Error("CONFIG_DIR env must be set")
		os.Exit(1)
	}
	result, err := os.ReadFile(path.Join(configDir, CLUSTERS_CFG_FILE))
	if err != nil {
		slog.Error("cannot read clusters config", "config-file", CLUSTERS_CFG_FILE, "error", err)
		os.Exit(4)
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=\"clusters.csv\"")
	_, err = w.Write(result)
	if err != nil {
		slog.Error("Error writing data to response", "error", err)
		return
	}
}

// TODO: implement
func (e *engine) RestartRecorder(w http.ResponseWriter, r *http.Request) {
	shootNamespace := r.PathValue("shootNamespace")
	if len(shootNamespace) == 0 {
		slog.Error("error fetching path value from URL.")
	}

	runningRecorder := recorder.GetRecorders(shootNamespace)

	//Read kubeconfigs from body
	// TODO: multipart request
	var k kubeconfigs
	err := json.NewDecoder(r.Body).Decode(&k)
	if err != nil {
		return
	}

	// Update kubeconfigs
	configDir := os.Getenv("CONFIG_DIR")
	if len(configDir) == 0 {
		slog.Error("CONFIG_DIR env must be set")
		os.Exit(1)
	}
	result, err := os.ReadFile(path.Join(configDir, CLUSTERS_CFG_FILE))
	if err != nil {
		slog.Error("cannot read clusters config", "config-file", CLUSTERS_CFG_FILE, "error", err)
		os.Exit(4)
	}
	reader := csv.NewReader(strings.NewReader(string(result)))
	reader.Comment = '#'
	records, err := reader.ReadAll()
	if err != nil {
		slog.Error("cannot read clusters config", "config-file", CLUSTERS_CFG_FILE, "error", err)
		os.Exit(5)
	}
	var clusterData []string

	for _, row := range records {
		if row[1] == shootNamespace {
			clusterData = row
			break
		}
	}

	// Write new kubeconfig for shoot
	err = os.WriteFile(clusterData[2], []byte(k.Shoot), 0664)
	if err != nil {
		return
	}

	// Write new kubeconfig for seed
	err = os.WriteFile(clusterData[3], []byte(k.Seed), 0664)
	if err != nil {
		return
	}

	// Get DB dir
	dbDir := os.Getenv("DB_DIR")
	if len(dbDir) == 0 {
		slog.Warn("DB_DIR is NOT set. Assuming same as CONFIG_DIR", "config_dir", configDir)
		dbDir = configDir
	}

	// Stop the recorder
	err = runningRecorder.Close()
	if err != nil {
		return
	}

	// Create a new RecorderParams
	newRecorderParams := gsh.RecorderParams{
		Landscape:           clusterData[0],
		ShootNameSpace:      clusterData[1],
		ShootKubeConfigPath: clusterData[2],
		SeedKubeConfigPath:  clusterData[3],
		DBDir:               dbDir,
	}

	startTime := time.Now()
	newRecorder, err := recorder.NewDefaultRecorder(newRecorderParams, startTime)
	if err != nil {
		slog.Error("cannot create new recorder", "error", err)
		os.Exit(3)
	}

	err = newRecorder.Start(e.ctx)
	slog.Info("STARTED recorder", "startTime", startTime, "params", newRecorderParams)
	if err != nil {
		slog.Error("cannot start recorder recorder", "error", err)
		os.Exit(4)
	}
}

func (e *engine) Shutdown(ctx context.Context) error {
	if err := e.httpServer.Shutdown(ctx); err != nil {
		slog.Error("error shutting down scenario http server", "error", err)
		return err
	}
	return nil
}
