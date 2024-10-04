package main

import (
	"flag"
	"fmt"
	"github.com/elankath/gardener-scaling-history/comparer"
	"os"
)

func main() {
	c, err := parseArgs()
	comparer.DieOnError(err, "error parsing args")

	result, err := comparer.GenerateReportFromConfig(c)
	if err != nil {
		comparer.DieOnError(err, "error generating report")
	}

	fmt.Printf("Generated comparison reports at %v\n", result)
}

func parseArgs() (comparer.Config, error) {
	c := comparer.Config{}
	args := os.Args[1:]
	fs := flag.CommandLine
	fs.StringVar(&c.Provider, "provider", "aws", "cloud provider")
	fs.StringVar(&c.CAReportPath, "ca-report-path", "", "CA report path")
	fs.StringVar(&c.SRReportPath, "sr-report-path", "", "SR report path")
	fs.StringVar(&c.ReportOutDir, "report-out-dir", "/tmp", "Generated reports directory")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	return c, nil
}
