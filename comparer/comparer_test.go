package comparer

import (
	assert "github.com/stretchr/testify/require"
	"testing"
)

func TestGenerateReport(t *testing.T) {
	//replayReportPairs, err := apputil.ListAllReplayReportPairs("/tmp")
	//assert.NoError(t, err)
	//if len(replayReportPairs) == 0 {
	//	t.Fatalf("kindly download some replay reports into /tmp using ./hack/download-report.sh")
	//	return
	//}
	//for _, v := range replayReportPairs {
	//	//htmlReportPath := GetHTMLReportPath("/tmp", v[0])
	//}
	c := Config{
		CAReportPath: "/tmp/canary_hc-dev_demo-hc-3-haas_ca-replay-1.json",
		SRReportPath: "/tmp/canary_hc-dev_demo-hc-3-haas_sr-replay-1.json",
		ReportOutDir: "/tmp",
	}
	result, err := GenerateReportFromConfig(c)
	assert.NoError(t, err)
	t.Logf("Generated report into %s", result.HTMLReportPath)
}
