package main

import (
	"context"
	"github.com/elankath/scalehist/analyzer"
	"github.com/elankath/scalehist/reporter"
	"log"
	"log/slog"
	"os"
	"time"
)

func main() {
	dataDbPath := os.Getenv("DB_PATH")
	if len(dataDbPath) == 0 {
		slog.Error("DB_PATH env must be set")
		os.Exit(2)
	}
	reportDir := os.Getenv("REPORT_DIR")
	if len(reportDir) == 0 {
		slog.Error("REPORT_DIR env must be set")
		os.Exit(2)
	}
	scenarioCoalesceIntervalStr := os.Getenv("COALESCE_INTERVAL")
	if len(scenarioCoalesceIntervalStr) == 0 {
		slog.Error("SCENARIO_DIVIDE_INTERVAL env must be set")
		os.Exit(2)
	}
	scenarioCoalesceInterval, err := time.ParseDuration(scenarioCoalesceIntervalStr)
	if err != nil {
		slog.Error("SCENARIO_DIVIDE_INTERVAL  must be a parsable time.Duration")
		os.Exit(2)
	}

	scenarioTolerationIntervalStr := os.Getenv("TOLERATION_INTERVAL")
	if len(scenarioCoalesceIntervalStr) == 0 {
		slog.Error("COALESCE_INTERVAL env must be set")
		os.Exit(2)
	}
	scenarioTolerationInterval, err := time.ParseDuration(scenarioTolerationIntervalStr)
	if err != nil {
		slog.Error("TOLERATION_INTERVAL  must be a parsable time.Duration")
		os.Exit(2)
	}
	//	reporterParams := scalehist.ReporterParams{DataDBPath: dataDbPath, ReportDir: reportDir}
	defaultAnalyzer, err := analyzer.NewDefaultAnalyzer(dataDbPath, scenarioCoalesceInterval, scenarioTolerationInterval)
	//ctx, cancelFunc := context.WithCancel(context.Background())
	analysis, err := defaultAnalyzer.Analyze(context.Background()) //TODO: maybe pass the start time
	if err != nil {
		slog.Error("analysis failed", "error", err)
		os.Exit(3)
	}
	defaultReporter, err := reporter.NewReporter(reportDir)
	reportPath, err := defaultReporter.GenerateTextReport(analysis)
	if err != nil {
		log.Fatal(err)
	}
	slog.Info("Generated Text Report.", "reportPath", reportPath)

	reportPath, err = defaultReporter.GenerateJsonReport(analysis)
	if err != nil {
		log.Fatal(err)
	}
	slog.Info("Generated Json Report.", "reportPath", reportPath)
}
