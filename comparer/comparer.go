package comparer

import (
	"bytes"
	"cmp"
	"embed"
	"encoding/json"
	"fmt"
	gsc "github.com/elankath/gardener-scaling-common"
	gsh "github.com/elankath/gardener-scaling-history"
	"github.com/elankath/gardener-scaling-history/apputil"
	"github.com/elankath/gardener-scaling-history/pricing"
	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
	md "github.com/nao1215/markdown"
	"github.com/samber/lo"
	"github.com/yuin/goldmark"
	"golang.org/x/exp/maps"
	"html/template"
	"k8s.io/apimachinery/pkg/api/resource"
	"log/slog"
	"math"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

//go:embed page.html
var embedContent embed.FS
var pageTemplatePath = "page.html"
var pageTemplate *template.Template

func init() {
	var err error
	pageTemplate, err = template.ParseFS(embedContent, pageTemplatePath)
	if err != nil {
		panic(fmt.Errorf("cannot parse template %q: %w", pageTemplatePath, err))
	}
}

type Config struct {
	CAReportPath string
	SRReportPath string
	Provider     string
	ReportOutDir string
}

type Result struct {
	MDReportPath   string
	HTMLReportPath string
}

func DieOnError(err error, msg string) {
	if err != nil {
		slog.Error(msg, "error", err)
		os.Exit(1)
	}
}

func UnmarshallReport(path string) (gsh.Scenario, error) {
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

func mapToScalingRows(pa pricing.InstancePricingAccess, s gsh.Scenario) []ScalingRow {
	nodeGroups := s.ScalingResult.ScaledUpNodeGroups
	rows := make([]ScalingRow, 0, len(nodeGroups))
	for ng, count := range nodeGroups {
		instanceType, cpu, memory, cost := getInstanceDetailsForNodeGroup(pa, s.ClusterSnapshot.AutoscalerConfig.NodeTemplates, ng)
		if instanceType == "" {
			continue
		}
		rows = append(rows, ScalingRow{Name: ng, Count: count, InstanceType: instanceType, Cost: fmt.Sprintf("$%.2f", cost), CPU: cpu.String(), Memory: fmt.Sprintf("%.2f GiB", float64(memory.Value())/1024/1024/1024)})
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

// rows = append(rows, []string{ng, fmt.Sprint(count), instanceType, fmt.Sprintf("$%.2f", cost), cpu.String(), fmt.Sprintf("%.2f GiB", float64(memory.Value())/1024/1024/1024)})
type ScalingRow struct {
	Name         string
	Count        int
	InstanceType string
	Cost         string
	CPU          string
	Memory       string
}

type TotalCosts struct {
	VirtualCA          float64
	ScalingRecommender float64
	Savings            float64
}

type HTMLPageData struct {
	Title               string
	ClusterName         string
	Provider            string
	CASettings          gsc.CASettingsInfo
	UnscheduledPodCount int
	NodeGroups          []gsc.NodeGroupInfo
	CAScaleUpRows       []ScalingRow
	SRScaleUpRows       []ScalingRow
	TotalCosts          TotalCosts
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

func GenerateReportFromConfig(c Config) (res Result, err error) {
	caScenario, err := UnmarshallReport(c.CAReportPath)
	if err != nil {
		err = fmt.Errorf("error unmarshalling CA report: %w", err)
		return
	}
	srScenario, err := UnmarshallReport(c.SRReportPath)
	if err != nil {
		err = fmt.Errorf("error unmarshalling SR report: %w", err)
		return
	}
	if c.Provider == "" {
		c.Provider, err = apputil.GuessProvider(caScenario)
		if err != nil {
			err = fmt.Errorf("error guessing provider: %w", err)
			return
		}
	}
	priceAccess, err := pricing.NewInstancePricingAccess(c.Provider)
	if err != nil {
		err = fmt.Errorf("error creating instance pricing access for provider %s: %w", c.Provider, err)
		return
	}
	clusterName, err := apputil.GetClusterName(c.CAReportPath)
	if err != nil {
		err = fmt.Errorf("error getting cluster name from ca report path %q: %w", c.CAReportPath, err)
		return
	}
	reportSuffix := getReportIndex(c.CAReportPath)
	res, err = GenerateReport(priceAccess, c.Provider, clusterName, reportSuffix, c.ReportOutDir, caScenario, srScenario)
	return
}

func GenerateReport(pa pricing.InstancePricingAccess, provider, clusterName, reportSuffix string, reportOutDir string,
	caScenario, srScenario gsh.Scenario) (res Result, err error) {

	adjustScenario(&caScenario)
	adjustScenario(&srScenario)

	vcaTotalScaleupCost, err := sumInstancePrices(pa, caScenario.ScalingResult.ScaledUpNodes)
	if err != nil {
		return
	}
	srTotalScaleupCost, err := sumInstancePrices(pa, srScenario.ScalingResult.ScaledUpNodes)
	if err != nil {
		return
	}
	caResourceStats, err := caScenario.GetResourceStat()
	if err != nil {
		return
	}
	srResourceStats, err := srScenario.GetResourceStat()
	if err != nil {
		return
	}

	res.MDReportPath = filepath.Join(reportOutDir, fmt.Sprintf("%s-%s.md", clusterName, reportSuffix))
	targetMDFile, err := os.OpenFile(res.MDReportPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return
	}

	mkBuilder := md.NewMarkdown(targetMDFile).
		H1("Virtual CA vs Scaling Recommender Comparison Report").LF().
		PlainTextf("Cluster: %s", clusterName).
		PlainTextf("\nProvider: %s", provider).
		PlainTextf("\nUnscheduled Pod Count: %v", getUnscheduledPodCount(caScenario)). //TODO: Placeholder only, needs to be changed to counts after stabilization interval
		H2("Scaled-Up NodeGroups").LF().
		PlainText("This section compares the node groups that are scaled by Virtual CA and Scaling Recommender").
		H3("Virtual CA").
		LF().
		Table(md.TableSet{
			Header: []string{"NodeGroup", "Count", "Instance Type", "Cost of Instance", "Cpu", "Memory"},
			Rows:   mapToRows(pa, caScenario),
		}).
		H3("Scaling Recommender").
		LF().
		Table(md.TableSet{
			Header: []string{"NodeGroup", "Count", "Instance Type", "Cost of Instance", "Cpu", "Memory"},
			Rows:   mapToRows(pa, srScenario),
		}).
		H2("Total Cost of Scaled-Up Nodes").
		LF().
		PlainText("This section compares the total cost of the scaled-up nodes by Virtual CA and Scaling Recommender").
		LF().
		Table(md.TableSet{
			Header: []string{"Virtual CA", "Scaling Recommender", "Saved Costs"},
			Rows: [][]string{
				{fmt.Sprintf("$%.2f", vcaTotalScaleupCost), fmt.Sprintf("$%.2f", srTotalScaleupCost), fmt.Sprintf("$%.2f", vcaTotalScaleupCost-srTotalScaleupCost)},
			},
		}).
		H2("Resource Utilization").
		LF().
		PlainText("This section compares the resource utilization by Virtual CA and Scaling Recommender for scaled up nodes").
		H3("Virtual CA").
		LF().
		PlainText(md.Bold("Resource: CPU")).
		LF().
		BulletList(
			fmt.Sprintf("*Total Allocated CPU:* %s", caResourceStats.AvailAllocCPU.String()),
			fmt.Sprintf("*Total Utilized CPU:* %s", caResourceStats.TotalUtilCPU.String()),
			fmt.Sprintf("*Total Allocated Memory:* %.2f GiB", float64(caResourceStats.AvailAllocMem.Value())/1024/1024/1024),
			fmt.Sprintf("*Total Utilized Memory:* %.2f GiB", float64(caResourceStats.TotalUtilMem.Value())/1024/1024/1024)).
		LF().
		PlainTextf("**CPU Utilization Percentage:** %.2f%%", 100*caResourceStats.TotalUtilCPU.AsApproximateFloat64()/caResourceStats.AvailAllocCPU.AsApproximateFloat64()).
		LF().
		PlainTextf("**Memory Utilization Percentage:** %.2f%%", 100*caResourceStats.TotalUtilMem.AsApproximateFloat64()/caResourceStats.AvailAllocMem.AsApproximateFloat64()).
		LF().
		H3("Scaling Recommender").
		LF().
		PlainText(md.Bold("Resource: CPU")).
		LF().
		BulletList(
			fmt.Sprintf("*Total Allocated CPU:* %s", srResourceStats.AvailAllocCPU.String()),
			fmt.Sprintf("*Total Utilized CPU:* %s", srResourceStats.TotalUtilCPU.String()),
			fmt.Sprintf("*Total Allocated Memory:* %.2f GiB", float64(srResourceStats.AvailAllocMem.Value())/1024/1024/1024),
			fmt.Sprintf("*Total Utilized Memory:* %.2f GiB", float64(srResourceStats.TotalUtilMem.Value())/1024/1024/1024)).
		LF().
		PlainTextf("**CPU Utilization Percentage:** %.2f%%", 100*srResourceStats.TotalUtilCPU.AsApproximateFloat64()/srResourceStats.AvailAllocCPU.AsApproximateFloat64()).
		LF().
		PlainTextf("**Memory Utilization Percentage:** %.2f%%", 100*srResourceStats.TotalUtilMem.AsApproximateFloat64()/srResourceStats.AvailAllocMem.AsApproximateFloat64()).
		LF().
		H2("Unscheduled Pods").
		LF().
		PlainText("This section compares the unscheduled pods post scaling attempts by Virtual CA and Scaling Recommender").
		H3("Virtual CA").
		LF().
		PlainTextf("*Count:* %d", len(caScenario.ScalingResult.PendingUnscheduledPods)).LF()

	if len(caScenario.ScalingResult.PendingUnscheduledPods) > 0 {
		mkBuilder.BulletList(pendingUnscheduledPodNames(caScenario)...)
	}
	mkBuilder.H3("Scaling Recommender").LF().
		PlainTextf("*Count:* %d", len(srScenario.ScalingResult.PendingUnscheduledPods))

	if len(srScenario.ScalingResult.PendingUnscheduledPods) > 0 {
		mkBuilder.LF().BulletList(pendingUnscheduledPodNames(srScenario)...).LF()
	}

	err = mkBuilder.Build()
	if err != nil {
		return
	}

	var buf bytes.Buffer
	res.HTMLReportPath = GetHTMLReportPath(reportOutDir, clusterName, reportSuffix)
	nodeGroups := maps.Values(caScenario.ClusterSnapshot.AutoscalerConfig.NodeGroups)
	slices.SortFunc(nodeGroups, func(a, b gsc.NodeGroupInfo) int {
		return cmp.Compare(a.Name, b.Name)
	})

	caScalingRows := mapToScalingRows(pa, caScenario)
	srScalingRows := mapToScalingRows(pa, srScenario)
	pageData := HTMLPageData{
		Title:               "Replay: " + clusterName + ":" + reportSuffix,
		ClusterName:         clusterName,
		Provider:            provider,
		CASettings:          caScenario.ClusterSnapshot.AutoscalerConfig.CASettings,
		UnscheduledPodCount: getUnscheduledPodCount(caScenario),
		NodeGroups:          nodeGroups,
		CAScaleUpRows:       caScalingRows,
		SRScaleUpRows:       srScalingRows,
		TotalCosts: TotalCosts{
			VirtualCA:          math.Round(vcaTotalScaleupCost),
			ScalingRecommender: math.Round(srTotalScaleupCost),
		},
	}
	pageData.TotalCosts.Savings = pageData.TotalCosts.VirtualCA - pageData.TotalCosts.ScalingRecommender
	// Tabs: Overview, CA vs SR Comparison, [CA Report JSON, SR Report JSON: option]
	err = pageTemplate.Execute(&buf, pageData)
	if err != nil {
		return
	}
	err = os.WriteFile(res.HTMLReportPath, buf.Bytes(), 0644)
	//err = mdToHTML(res.MDReportPath, res.HTMLReportPath)
	return
}

func mdToHTML(mdPath, htmlPath string) error {
	mdBytes, err := os.ReadFile(mdPath)
	if err != nil {
		return err
	}
	// create markdown parser with extensions
	extensions := parser.CommonExtensions | parser.AutoHeadingIDs | parser.NoEmptyLineBeforeBlock
	p := parser.NewWithExtensions(extensions)
	doc := p.Parse(mdBytes)

	// create HTML renderer with extensions
	htmlFlags := html.CommonFlags | html.HrefTargetBlank
	opts := html.RendererOptions{Flags: htmlFlags}
	renderer := html.NewRenderer(opts)

	htmlBytes := markdown.Render(doc, renderer)

	err = os.WriteFile(htmlPath, htmlBytes, 0644)
	if err != nil {
		return err
	}

	return nil
}

func mdToHTML2(mdPath, htmlPath string) error {
	mdBytes, err := os.ReadFile(mdPath)
	if err != nil {
		return err
	}
	var htmlBuf bytes.Buffer
	err = goldmark.Convert(mdBytes, &htmlBuf)
	if err != nil {
		return err
	}

	err = os.WriteFile(htmlPath, htmlBuf.Bytes(), 0644)
	if err != nil {
		return err
	}

	return nil
}

func GetHTMLReportPath(outDir, clusterName string, reportSuffix string) string {
	return filepath.Join(outDir, fmt.Sprintf("%s-%s.html", clusterName, reportSuffix))
}
