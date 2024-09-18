package main

import (
	"fmt"
	"github.com/dustin/go-humanize"
	gsc "github.com/elankath/gardener-scaling-common"
	gsh "github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/apputil"
	"github.com/elankath/gardener-scaling-history/pricing"
	"github.com/google/go-cmp/cmp"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/util/json"
	"log/slog"
	"os"
	"path"
	"slices"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: comparer CA_report_path SR_report_path")
		os.Exit(1)
	}

	caReportPath := os.Args[1]
	srReportPath := os.Args[2]
	if caReportPath == srReportPath {
		fmt.Println("CA reports are the same as SR reports")
		os.Exit(1)
	}

	var caReportScenario, srReportScenario gsh.Scenario

	data, err := os.ReadFile(caReportPath)
	if err != nil {
		slog.Error("Error reading CA report", "error", err, "path", caReportPath)
		os.Exit(2)
	}
	err = json.Unmarshal(data, &caReportScenario)
	if err != nil {
		slog.Error("Error unmarshalling CA report", "error", err, "path", caReportPath)
		os.Exit(3)
	}

	data, err = os.ReadFile(srReportPath)
	if err != nil {
		slog.Error("Error reading SR report", "error", err, "path", srReportPath)
		os.Exit(4)
	}
	err = json.Unmarshal(data, &srReportScenario)
	if err != nil {
		slog.Error("Error unmarshalling SR report", "error", err, "path", srReportPath)
		os.Exit(5)
	}
	clusterName, err := apputil.GetClusterName(caReportPath)
	if err != nil {
		slog.Error("Error getting cluster name from CA report path", "error", err, "path", caReportPath)
		os.Exit(6)
	}

	provider := "aws"
	if strings.Contains(clusterName, "-gc-") {
		provider = "gcp"
	}

	err = GenerateReport(provider, clusterName, caReportScenario, srReportScenario)
	if err != nil {
		slog.Error("Error generating report", "error", err)
		os.Exit(7)
	}
}

func GenerateReport(providerName string, clusterName string, caScenario, srScenario gsh.Scenario) error {
	reportPath := path.Join("/tmp", fmt.Sprintf("%s_compare-replay.md", clusterName))

	var sb strings.Builder

	fmt.Println(cmp.Diff(caScenario.ScalingResult.ScaledUpNodeGroups, srScenario.ScalingResult.ScaledUpNodeGroups))

	fmt.Fprintf(&sb, "# %s | CA vs SR\n", clusterName)

	fmt.Fprintln(&sb, "## Difference in ScaledUpNodeGroups")
	//ngDiff := cmp.Diff(caScenario.ScalingResult.ScaledUpNodeGroups, srScenario.ScalingResult.ScaledUpNodeGroups)
	//if ngDiff == "" {
	//	fmt.Fprintln(&sb, "Identical scale ups")
	//	fmt.Fprintln(&sb, "```")
	//	fmt.Fprintln(&sb, cmp.Diff(map[string]int{}, caScenario.ScalingResult.ScaledUpNodeGroups))
	//	fmt.Fprintln(&sb, "```")
	//} else {
	//	fmt.Fprintln(&sb, "```")
	//	fmt.Fprintln(&sb, ngDiff)
	//	fmt.Fprintln(&sb, "```")
	//}
	fmt.Fprintln(&sb, "### VCA ScaledUpNodeGroups")
	fmt.Fprintln(&sb, "```")
	fmt.Fprintln(&sb, caScenario.ScalingResult.ScaledUpNodeGroups)
	fmt.Fprintln(&sb, "```")

	fmt.Fprintln(&sb, "### SR ScaledUpNodeGroups")
	fmt.Fprintln(&sb, "```")
	fmt.Fprintln(&sb, srScenario.ScalingResult.ScaledUpNodeGroups)
	fmt.Fprintln(&sb, "```")

	priceAccess, err := pricing.NewInstancePricingAccess(providerName)
	if err != nil {
		slog.Error("Error creating instance pricing access", "error", err)
		return err
	}
	caTotalPrice, err := sumPrices(priceAccess, caScenario.ScalingResult.ScaledUpNodes)
	if err != nil {
		return err
	}
	srTotalPrice, err := sumPrices(priceAccess, srScenario.ScalingResult.ScaledUpNodes)
	if err != nil {
		return err
	}

	fmt.Fprintln(&sb, "---")

	fmt.Fprintln(&sb, "## Pricing")
	fmt.Fprintf(&sb, "* CA total price: $%.2f\n", caTotalPrice)
	fmt.Fprintf(&sb, "* SR total price: $%.2f\n", srTotalPrice)

	fmt.Fprintln(&sb, "---")

	fmt.Fprintln(&sb, "## Utilization")

	caStats, err := caScenario.ScalingResult.GetResourceStat()
	if err != nil {
		return err
	}
	srStats, err := srScenario.ScalingResult.GetResourceStat()
	if err != nil {
		return err
	}
	fmt.Fprintln(&sb, "### CA utilization")
	PrintStats(&sb, caStats)
	fmt.Fprintln(&sb, "### SR utilization")
	PrintStats(&sb, srStats)

	fmt.Fprintln(&sb, "---")

	fmt.Fprintln(&sb, "## PendingUnscheduledPods")

	fmt.Fprintln(&sb, "### VCA")
	fmt.Fprintln(&sb, "* count: ", len(caScenario.ScalingResult.PendingUnscheduledPods))
	caPodNames1 := lo.Map(caScenario.ScalingResult.PendingUnscheduledPods, func(item gsc.PodInfo, _ int) string {
		fmt.Println(item.Name)
		return item.Name
	})
	slices.Sort(caPodNames1)
	fmt.Fprintln(&sb, "```")
	fmt.Fprintln(&sb, caPodNames1)
	fmt.Fprintln(&sb, "```")

	fmt.Fprintln(&sb, "### SR")
	fmt.Fprintln(&sb, "* count: ", len(srScenario.ScalingResult.PendingUnscheduledPods))
	srPodNames1 := lo.Map(srScenario.ScalingResult.PendingUnscheduledPods, func(item gsc.PodInfo, _ int) string {
		fmt.Println(item.Name)
		return item.Name
	})
	slices.Sort(srPodNames1)
	fmt.Fprintln(&sb, "```")
	fmt.Fprintln(&sb, srPodNames1)
	fmt.Fprintln(&sb, "```")

	fmt.Fprintln(&sb, "---")

	//fmt.Fprintln(&sb, "## Difference in PendingUnscheduledPods")
	//caPodNames := lo.Map(caScenario.ScalingResult.PendingUnscheduledPods, func(item gsc.PodInfo, _ int) string {
	//	fmt.Println(item.Name)
	//	return item.Name
	//})
	//slices.Sort(caPodNames)
	//srPodNames := lo.Map(srScenario.ScalingResult.PendingUnscheduledPods, func(item gsc.PodInfo, _ int) string {
	//	fmt.Println(item.Name)
	//	return item.Name
	//})
	//slices.Sort(srPodNames)
	//if len(caScenario.ScalingResult.PendingUnscheduledPods) > 0 || len(srScenario.ScalingResult.PendingUnscheduledPods) > 0 {
	//	fmt.Fprintln(&sb, "```")
	//	unschPodsDiff := cmp.Diff(caPodNames, srPodNames)
	//	if unschPodsDiff == "" {
	//		fmt.Fprintln(&sb, "Identical PendingUnscheduledPods")
	//		fmt.Fprintln(&sb, caPodNames)
	//	} else {
	//		fmt.Fprintln(&sb, unschPodsDiff)
	//	}
	//	fmt.Fprintln(&sb, "```")
	//} else {
	//	fmt.Fprintln(&sb, "No pending unscheduled pods")
	//}

	fmt.Println(sb.String())

	err = os.WriteFile(reportPath, []byte(sb.String()), 0644)
	if err != nil {
		slog.Error("Error writing report", "error", err)
		return err
	}
	slog.Info("Generated report", "reportPath", reportPath)

	return nil
}

func sumPrices(priceAccess pricing.InstancePricingAccess, nodes []gsc.NodeInfo) (float64, error) {
	var totalPrice float64
	for _, node := range nodes {
		machineType, ok := gsc.GetLabelValue(node.Labels, []string{"node.kubernetes.io/instance-type", "beta.kubernetes.io/instance-type"})
		if !ok {
			slog.Error("error getting instance type label", "label", node.Labels, "labelValue", node.Labels)
			return 0, fmt.Errorf("error getting instance type label for node %s", node.Name)
		}
		totalPrice = totalPrice + priceAccess.Get3YearReservedPricing(machineType)
	}
	return totalPrice, nil
}

func PrintStats(sb *strings.Builder, stat gsh.ResourceStats) {
	_, _ = fmt.Fprintf(sb, "* TotalAvailAllocCPU: %s\n", stat.AvailAllocCPU.String())
	_, _ = fmt.Fprintf(sb, "* TotalUtilCPU: %s\n", stat.TotalUtilCPU.String())
	avaiAllocInBytes := stat.AvailAllocMem.Value()
	avaiAlloc := humanize.Bytes(uint64(avaiAllocInBytes))
	_, _ = fmt.Fprintf(sb, "* TotalAvailAllocMem: %s\n", avaiAlloc)
	totalUtilInBytes := stat.TotalUtilMem.Value()
	totalUtil := humanize.Bytes(uint64(totalUtilInBytes))
	_, _ = fmt.Fprintf(sb, "* TotalUtilMem: %s\n", totalUtil)
	fmt.Fprintln(sb, "```")
	fmt.Fprintf(sb, "CPU utilization percentage: %.2f\n", 100*stat.TotalUtilCPU.AsApproximateFloat64()/stat.AvailAllocCPU.AsApproximateFloat64())
	fmt.Fprintf(sb, "Memory utilization percentage: %.2f\n", 100*stat.TotalUtilMem.AsApproximateFloat64()/stat.AvailAllocMem.AsApproximateFloat64())
	fmt.Fprintln(sb, "```")
}
