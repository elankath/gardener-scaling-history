package app

import (
	assert "github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestGetReplayerPodYaml(t *testing.T) {
	now := time.Now()
	//podTemplate, err := specs.GetReplayerPodYamlTemplate()

	yaml, err := GetReplayerPodYaml("/tmp/bingo.db", "dummyuser", now)
	assert.Nil(t, err)
	t.Log(yaml)
}
