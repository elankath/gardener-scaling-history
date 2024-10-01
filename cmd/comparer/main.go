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
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/json"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
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

func mapToRows(pa pricing.InstancePricingAccess, s gsh.Scenario) [][]string {
	nodeGroups := s.ScalingResult.ScaledUpNodeGroups
	rows := make([][]string, 0, len(nodeGroups))
	for ng, count := range nodeGroups {
		instanceType, cpu, memory, cost := getInstanceDetailsForNodeGroup(pa, s.ClusterSnapshot.AutoscalerConfig.NodeTemplates, ng)
		if instanceType == "" {
			rows = append(rows, []string{ng, fmt.Sprint(count), "", "", "", ""})
		}
		rows = append(rows, []string{ng, fmt.Sprint(count), instanceType, fmt.Sprintf("$%.2f", cost), cpu.String(), fmt.Sprintf("%.2f GiB", float64(memory.Value())/1024/1024/1024)})
	}
	return rows
}

func pendingUnscheduledPodNames(s gsh.Scenario) []string {
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

// TODO: Placeholder only. Unscheduled pod counts need to be obtained for CA and SR separately after stabilization interval
func getUnscheduledPodCount(s gsh.Scenario) (count int) {
	count = lo.CountBy(s.ClusterSnapshot.Pods, func(item gsc.PodInfo) bool {
		if item.NodeName == "" {
			return true
		}
		return false
	})
	return count
}

type poolKey struct {
	poolName string
	zone     string
}

func adjustScenario(s *gsh.Scenario) {
	poolKeyMap := make(map[poolKey]gsc.NodeGroupInfo)
	for _, ng := range lo.Values(s.ClusterSnapshot.AutoscalerConfig.NodeGroups) {
		poolKeyMap[poolKey{poolName: ng.PoolName, zone: ng.Zone}] = ng
	}
	for _, emptyNodeName := range s.ScalingResult.EmptyNodeNames {
		node, ok := lo.Find(s.ScalingResult.ScaledUpNodes, func(n gsc.NodeInfo) bool {
			return n.Name == emptyNodeName
		})
		if !ok {
			continue
		}
		zone, zoneFound := gsc.GetZone(node.Labels)
		poolName, poolNameFound := gsc.GetPoolName(node.Labels)
		if !zoneFound || !poolNameFound {
			slog.Error("cannot find zone or pool bane in node labels", "node", node)
			continue
		}
		pk := poolKey{poolName: poolName, zone: zone}
		ng, ok := poolKeyMap[pk]
		if !ok {
			slog.Error("cannot find node group in autoscaler config", "poolKey", pk)
			continue
		}
		count, ok := s.ScalingResult.ScaledUpNodeGroups[ng.Name]
		if !ok {
			slog.Error("cannot find node group in scaled up node groups", "nodeGroup", ng.Name)
			continue
		}
		s.ScalingResult.ScaledUpNodeGroups[ng.Name] = count - 1
		s.ScalingResult.ScaledUpNodes = lo.Reject(s.ScalingResult.ScaledUpNodes, func(n gsc.NodeInfo, _ int) bool {
			return n.Name == emptyNodeName
		})
	}
}

func getReportIndex(fp string) string {
	fn := path.Base(fp)
	filePathWithoutExtension := strings.TrimSuffix(fn, filepath.Ext(fn))
	index := strings.LastIndex(filePathWithoutExtension, "-")
	if index == -1 {
		return "-1"
	}
	return filePathWithoutExtension[index+1:]
}

func getInstanceDetailsForNodeGroup(pa pricing.InstancePricingAccess, nodeTemplates map[string]gsc.NodeTemplate, ng string) (instanceName string, cpu resource.Quantity, memory resource.Quantity, cost float64) {
	for k, v := range nodeTemplates {
		if k == ng {
			return v.InstanceType, *v.Allocatable.Cpu(), *v.Allocatable.Memory(), pa.Get3YearReservedPricing(v.InstanceType)
		}
	}
	return "", resource.Quantity{}, resource.Quantity{}, 0.0
}

func generateReport(pa pricing.InstancePricingAccess, c config, caScenarioReport, srScenarioReport gsh.Scenario) error {
	clusterName, err := apputil.GetClusterName(c.caReportPath)
	dieOnError(err, "error getting cluster name from ca report path")

	adjustScenario(&caScenarioReport)
	adjustScenario(&srScenarioReport)

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

	targetPath := filepath.Join("/tmp", fmt.Sprintf("%s-%s.md", clusterName, getReportIndex(c.caReportPath)))
	targetFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	mkBuilder := md.NewMarkdown(targetFile).
		H1("Virtual CA vs Scaling Recommender Comparison Report").
		PlainTextf("Cluster: %s", clusterName).
		PlainTextf("\nProvider: %s", c.provider).
		PlainTextf("\nUnscheduled Pod Count: %v", getUnscheduledPodCount(caScenarioReport)). //TODO: Placeholder only, needs to be changed to counts after stabilization interval
		H2("Scaled-Up NodeGroups").
		PlainText("This section compares the node groups that are scaled by Virtual CA and Scaling Recommender").
		H3("Virtual CA").
		LF().
		Table(md.TableSet{
			Header: []string{"NodeGroup", "Count", "Instance Type", "Cost of Instance", "Cpu", "Memory"},
			Rows:   mapToRows(pa, caScenarioReport),
		}).
		H3("Scaling Recommender").
		LF().
		Table(md.TableSet{
			Header: []string{"NodeGroup", "Count", "Instance Type", "Cost of Instance", "Cpu", "Memory"},
			Rows:   mapToRows(pa, srScenarioReport),
		}).
		H2("Total Cost of Scaled-Up Nodes").
		PlainText("This section compares the total cost of the scaled-up nodes by Virtual CA and Scaling Recommender").
		LF().
		Table(md.TableSet{
			Header: []string{"Virtual CA", "Scaling Recommender", "Saved Costs"},
			Rows: [][]string{
				{fmt.Sprintf("$%.2f", vcaTotalScaleupCost), fmt.Sprintf("$%.2f", srTotalScaleupCost), fmt.Sprintf("$%.2f", vcaTotalScaleupCost-srTotalScaleupCost)},
			},
		}).
		H2("Resource Utilization").
		PlainText("This section compares the resource utilization by Virtual CA and Scaling Recommender for scaled up nodes").
		H3("Virtual CA").
		PlainText(md.Bold("Resource: CPU")).
		BulletList(
			fmt.Sprintf("*Total Allocated CPU:* %s", caResourceStats.AvailAllocCPU.String()),
			fmt.Sprintf("*Total Utilized CPU:* %s", caResourceStats.TotalUtilCPU.String()),
			fmt.Sprintf("*Total Allocated Memory:* %.2f GiB", float64(caResourceStats.AvailAllocMem.Value())/1024/1024/1024),
			fmt.Sprintf("*Total Utilized Memory:* %.2f GiB", float64(caResourceStats.TotalUtilMem.Value())/1024/1024/1024)).
		LF().
		PlainTextf("**CPU Utilization Percentage:** %.2f%%", 100*caResourceStats.TotalUtilCPU.AsApproximateFloat64()/caResourceStats.AvailAllocCPU.AsApproximateFloat64()).
		PlainTextf("**Memory Utilization Percentage:** %.2f%%", 100*caResourceStats.TotalUtilMem.AsApproximateFloat64()/caResourceStats.AvailAllocMem.AsApproximateFloat64()).
		H3("Scaling Recommender").
		PlainText(md.Bold("Resource: CPU")).
		BulletList(
			fmt.Sprintf("*Total Allocated CPU:* %s", srResourceStats.AvailAllocCPU.String()),
			fmt.Sprintf("*Total Utilized CPU:* %s", srResourceStats.TotalUtilCPU.String()),
			fmt.Sprintf("*Total Allocated Memory:* %.2f GiB", float64(srResourceStats.AvailAllocMem.Value())/1024/1024/1024),
			fmt.Sprintf("*Total Utilized Memory:* %.2f GiB", float64(srResourceStats.TotalUtilMem.Value())/1024/1024/1024)).
		LF().
		PlainTextf("**CPU Utilization Percentage:** %.2f%%", 100*srResourceStats.TotalUtilCPU.AsApproximateFloat64()/srResourceStats.AvailAllocCPU.AsApproximateFloat64()).
		PlainTextf("**Memory Utilization Percentage:** %.2f%%", 100*srResourceStats.TotalUtilMem.AsApproximateFloat64()/srResourceStats.AvailAllocMem.AsApproximateFloat64()).
		H2("Unscheduled Pods").
		PlainText("This section compares the unscheduled pods post scaling attempts by Virtual CA and Scaling Recommender").
		H3("Virtual CA").
		PlainTextf("*Count:* %d", len(caScenarioReport.ScalingResult.PendingUnscheduledPods))

	if len(caScenarioReport.ScalingResult.PendingUnscheduledPods) > 0 {
		mkBuilder.BulletList(pendingUnscheduledPodNames(caScenarioReport)...)
	}
	mkBuilder.H3("Scaling Recommender").
		PlainTextf("*Count:* %d", len(srScenarioReport.ScalingResult.PendingUnscheduledPods))

	if len(srScenarioReport.ScalingResult.PendingUnscheduledPods) > 0 {
		mkBuilder.BulletList(pendingUnscheduledPodNames(srScenarioReport)...)
	}

	return mkBuilder.Build()
}
