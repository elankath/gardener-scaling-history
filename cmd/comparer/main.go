package main

import (
	"flag"
	"fmt"
	gsc "github.com/elankath/gardener-scaling-common"
	gsh "github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/apputil"
	"github.com/elankath/gardener-scaling-history/pricing"
	md "github.com/nao1215/markdown"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/util/json"
	"log/slog"
	"os"
	"slices"
)

type config struct {
	caReportPath string
	srReportPath string
	provider     string
}

func main() {
	c, err := parseArgs()
	dieOnError(err, "error parsing args")

	caScenario, err := unmarshallReport(c.caReportPath)
	dieOnError(err, "error unmarshalling CA report")
	srScenario, err := unmarshallReport(c.srReportPath)
	dieOnError(err, "error unmarshalling SR report")
	priceAccess, err := pricing.NewInstancePricingAccess(c.provider)
	dieOnError(err, "error creating instance pricing access")

	dieOnError(generateReport(priceAccess, c, caScenario, srScenario), "error generating report")
}

func parseArgs() (config, error) {
	c := config{}
	args := os.Args[1:]
	fs := flag.CommandLine
	fs.StringVar(&c.provider, "provider", "aws", "cloud provider")
	fs.StringVar(&c.caReportPath, "ca-report-path", "", "CA report path")
	fs.StringVar(&c.srReportPath, "sr-report-path", "", "SR report path")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	return c, nil
}

func dieOnError(err error, msg string) {
	if err != nil {
		slog.Error(msg, err)
		os.Exit(1)
	}
}

func unmarshallReport(path string) (gsh.Scenario, error) {
	var scenario gsh.Scenario
	reportData, err := os.ReadFile(path)
	if err != nil {
		return gsh.Scenario{}, err
	}
	if err = json.Unmarshal(reportData, &scenario); err != nil {
		return gsh.Scenario{}, err
	}
	return scenario, nil
}

func mapToRows(m map[string]int) [][]string {
	rows := make([][]string, 0, len(m))
	for k, v := range m {
		rows = append(rows, []string{k, fmt.Sprint(v)})
	}
	return rows
}

func unscheduledPodNames(s gsh.Scenario) []string {
	podNames := lo.Map(s.ScalingResult.PendingUnscheduledPods, func(item gsc.PodInfo, _ int) string {
		return item.Name
	})
	slices.Sort(podNames)
	return podNames
}

func sumInstancePrices(pa pricing.InstancePricingAccess, nodes []gsc.NodeInfo) (float64, error) {
	var totalPrice float64
	for _, node := range nodes {
		machineType, ok := gsc.GetLabelValue(node.Labels, []string{"node.kubernetes.io/instance-type", "beta.kubernetes.io/instance-type"})
		if !ok {
			slog.Error("error getting instance type label", "label", node.Labels, "labelValue", node.Labels)
			return 0, fmt.Errorf("error getting instance type label for node %s", node.Name)
		}
		totalPrice = totalPrice + pa.Get3YearReservedPricing(machineType)
	}
	return totalPrice, nil
}

func generateReport(pa pricing.InstancePricingAccess, c config, caScenarioReport, srScenarioReport gsh.Scenario) error {
	clusterName, err := apputil.GetClusterName(c.caReportPath)
	dieOnError(err, "error getting cluster name from ca report path")

	vcaTotalScaleupCost, err := sumInstancePrices(pa, caScenarioReport.ScalingResult.ScaledUpNodes)
	if err != nil {
		return err
	}
	srTotalScaleupCost, err := sumInstancePrices(pa, srScenarioReport.ScalingResult.ScaledUpNodes)
	if err != nil {
		return err
	}
	caResourceStats, err := caScenarioReport.ScalingResult.GetResourceStat()
	if err != nil {
		return err
	}
	srResourceStats, err := srScenarioReport.ScalingResult.GetResourceStat()
	if err != nil {
		return err
	}

	mkBuilder := md.NewMarkdown(os.Stdout).
		H1("Virtual CA vs Scaling Recommender Comparison Report").
		PlainTextf("Cluster: %s", clusterName).
		PlainTextf("Provider: %s", c.provider).
		H2("Scaled-Up NodeGroups").
		PlainText("This section compares the node groups that are scaled by Virtual CA and Scaling Recommender").
		H3("Virtual CA").
		Table(md.TableSet{
			Header: []string{"NodeGroup", "Count"},
			Rows:   mapToRows(caScenarioReport.ScalingResult.ScaledUpNodeGroups),
		}).
		H3("Scaling Recommender").
		Table(md.TableSet{
			Header: []string{"NodeGroup", "Count"},
			Rows:   mapToRows(srScenarioReport.ScalingResult.ScaledUpNodeGroups),
		}).
		H2("Total Cost of Scaled-Up Nodes").
		PlainText("This section compares the total cost of the scaled-up nodes by Virtual CA and Scaling Recommender").
		Table(md.TableSet{
			Header: []string{"Virtual CA", "Scaling Recommender"},
			Rows: [][]string{
				{fmt.Sprintf("$%.2f", vcaTotalScaleupCost), fmt.Sprintf("$%.2f", srTotalScaleupCost)},
			},
		}).
		H2("Resource Utilization").
		PlainText("This section compares the resource utilization by Virtual CA and Scaling Recommender for scaled up nodes").
		H3("Virtual CA").
		PlainText(md.Bold("Resource: CPU")).
		BulletList(
			fmt.Sprintf("*Total Allocated CPU:* %s", caResourceStats.AvailAllocCPU.String()),
			fmt.Sprintf("*Total Utilized CPU:* %s", caResourceStats.TotalUtilCPU.String()),
			fmt.Sprintf("*Total Allocated Memory:* %s", caResourceStats.AvailAllocMem.String()),
			fmt.Sprintf("*Total Utilized Memory:* %s", caResourceStats.TotalUtilMem.String())).
		LF().
		PlainTextf("**CPU Utilization Percentage:** %.2f%%", 100*caResourceStats.TotalUtilCPU.AsApproximateFloat64()/caResourceStats.AvailAllocCPU.AsApproximateFloat64()).
		PlainTextf("**Memory Utilization Percentage:** %.2f%%", 100*caResourceStats.TotalUtilMem.AsApproximateFloat64()/caResourceStats.AvailAllocMem.AsApproximateFloat64()).
		H3("Scaling Recommender").
		PlainText(md.Bold("Resource: CPU")).
		BulletList(
			fmt.Sprintf("*Total Allocated CPU:* %s", srResourceStats.AvailAllocCPU.String()),
			fmt.Sprintf("*Total Utilized CPU:* %s", srResourceStats.TotalUtilCPU.String()),
			fmt.Sprintf("*Total Allocated Memory:* %s", srResourceStats.AvailAllocMem.String()),
			fmt.Sprintf("*Total Utilized Memory:* %s", srResourceStats.TotalUtilMem.String())).
		LF().
		PlainTextf("**CPU Utilization Percentage:** %.2f%%", 100*srResourceStats.TotalUtilCPU.AsApproximateFloat64()/srResourceStats.AvailAllocCPU.AsApproximateFloat64()).
		PlainTextf("**Memory Utilization Percentage:** %.2f%%", 100*srResourceStats.TotalUtilMem.AsApproximateFloat64()/srResourceStats.AvailAllocMem.AsApproximateFloat64()).
		H2("Unscheduled Pods").
		PlainText("This section compares the unscheduled pods post scaling attempts by Virtual CA and Scaling Recommender").
		H3("Virtual CA").
		PlainTextf("*Count:* %d", len(caScenarioReport.ScalingResult.PendingUnscheduledPods))

	if len(caScenarioReport.ScalingResult.PendingUnscheduledPods) > 0 {
		mkBuilder.BulletList(unscheduledPodNames(caScenarioReport)...)
	}
	mkBuilder.H3("Scaling Recommender").
		PlainTextf("*Count:* %d", len(srScenarioReport.ScalingResult.PendingUnscheduledPods))

	if len(srScenarioReport.ScalingResult.PendingUnscheduledPods) > 0 {
		mkBuilder.BulletList(unscheduledPodNames(srScenarioReport)...)
	}

	return mkBuilder.Build()
}
