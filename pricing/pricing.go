package pricing

import (
	"embed"
	"fmt"
	"log/slog"
	"path/filepath"

	"k8s.io/apimachinery/pkg/util/json"
)

// assets is a `embed.FS` to embed all files in the `assets` directory
//
//go:embed assets/*.json
var assets embed.FS

type InstancePricingAccess interface {
	Get3YearReservedPricing(instanceType string) float64
	GetOnDemandPricing(instanceType string) float64
}

func NewInstancePricingAccess(provider string) (InstancePricingAccess, error) {
	a := &access{provider: provider}
	if err := a.initializeProviderPricing(); err != nil {
		return nil, err
	}
	return a, nil
}

type access struct {
	provider   string
	pricingMap map[string]InstancePricing
}

func (a *access) Get3YearReservedPricing(instanceType string) float64 {
	price, ok := a.pricingMap[instanceType]
	if !ok {
		slog.Error("instance type not found in pricing map", "instanceType", instanceType)
		return 0
	}
	return float64(price.EDPPrice.Reserved3Year)
}

func (a *access) GetOnDemandPricing(instanceType string) float64 {
	price, ok := a.pricingMap[instanceType]
	if !ok {
		slog.Error("instance type not found in pricing map", "instanceType", instanceType)
		return 0
	}
	return float64(price.EDPPrice.PayAsYouGo)
}

func (a *access) initializeProviderPricing() (err error) {
	switch a.provider {
	case "aws":
		a.pricingMap, err = loadInstancePricing(filepath.Join("assets", "aws_pricing_eu-west-1.json"))
	case "gcp":
		a.pricingMap, err = loadInstancePricing(filepath.Join("assets", "gcp_pricing_eu-west1.json"))
	default:
		err = fmt.Errorf("provider not supported: %s", a.provider)
	}
	return
}

func loadInstancePricing(pricingJsonPath string) (map[string]InstancePricing, error) {
	var allPricing AllInstancePricing
	content, err := assets.ReadFile(pricingJsonPath)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(content, &allPricing); err != nil {
		return nil, err
	}

	pricingMap := make(map[string]InstancePricing)
	for _, pricing := range allPricing.Results {
		pricingMap[pricing.InstanceType] = pricing
	}
	return pricingMap, nil
}
