package specs

import "embed"

// assets is a `embed.FS` to embed all files in the `assets` directory
//
//go:embed *.yaml
var assets embed.FS

func GetReplayerPodYamlTemplate() (string, error) {
	data, err := assets.ReadFile("replayer.yaml")
	if err != nil {
		return "", err
	}

	return string(data), nil
}
