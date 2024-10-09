package app

import (
	assert "github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestGetReplayerPodYaml(t *testing.T) {
	now := time.Now()
	//podTemplate, err := specs.GetReplayerPodYamlTemplate()
	yaml, err := GetCAReplayerPodYaml("/tmp/bingo.db", "dummyuser", now)
	assert.Nil(t, err)
	t.Log(yaml)
}

func TestGetClusterNameForPod(t *testing.T) {
	dbPath := "/bingo/tringo/live_abap_prod-us30-1.db"
	caPodName := GetCAPodName(dbPath)
	t.Logf("caPodName: %s", caPodName)
	assert.Equal(t, "scaling-history-replayer-ca-live-abap-prod-us30-1", caPodName)
}
func TestGetClusterNameForPodFromCAReport(t *testing.T) {
	reportPath := "/data/reports/live_abap_prod-us30-1_ca-replay-4.json"
	srPodName := GetSRPodName(reportPath)
	t.Logf("srPodName: %s", srPodName)
}
