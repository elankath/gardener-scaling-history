package apputil

import (
	assert "github.com/stretchr/testify/require"
	"testing"
)

func TestGetSRReportPath(t *testing.T) {
	srReportPath := GetSRReportPath("/tmp", "live_hct-us10_prod-hdl_ca-replay-10.json")
	assert.Equal(t, "/tmp/live_hct-us10_prod-hdl_sr-replay-10.json", srReportPath)
}
