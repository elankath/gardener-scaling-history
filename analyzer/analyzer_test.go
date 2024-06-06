package analyzer

import (
	"fmt"
	"github.com/elankath/gardener-scalehist"
	"github.com/samber/lo"
	assert "github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/json"
	"os"
	"testing"
)

func TestFilterUnscheduledPodsForScenario(t *testing.T) {
	unscheduledPods := []scalehist.PodInfo{
		{
			Name: "pod-a",
		},
		{
			Name: "pod-b",
		},
		{
			Name: "pod-c",
		},
	}
	scheduledPodNames := []string{
		"pod-b",
	}
	expected := []scalehist.PodInfo{
		unscheduledPods[0],
		unscheduledPods[2],
	}
	actual := filterUnscheduledPodsForScenario(unscheduledPods, scheduledPodNames)
	assert.Equal(t, expected, actual)
}

func TestDifference(t *testing.T) {
	podKeys := []scalehist.PodInfoKey{
		{
			UID:  "1",
			Name: "A",
			Hash: "h1",
		},
		{
			UID:  "2",
			Name: "B",
			Hash: "h2",
		},
		{
			UID:  "3",
			Name: "C",
			Hash: "h3",
		},
		{
			UID:  "4",
			Name: "D",
			Hash: "h4",
		},
	}
	l1podKeys := []scalehist.PodInfoKey{
		podKeys[0],
		podKeys[2],
		podKeys[3],
	}
	l2podKeys := []scalehist.PodInfoKey{
		podKeys[2],
		podKeys[3],
	}
	d1, d2 := lo.Difference(l1podKeys, l2podKeys)
	fmt.Printf("Diff1 : %v\n Diff2 : %v\n", d1, d2)
	assert.Equal(t, []scalehist.PodInfoKey{}, d2)
	//expectedDiff1 := []scalehist.PodInfoKey{}
}

func Test_getBaseName(t *testing.T) {
	type args struct {
		filepath string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "simple",
			args: args{filepath: "/tmp/bingo.db"},
			want: "bingo",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getBaseName(tt.args.filepath); got != tt.want {
				t.Errorf("getBaseName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSumResources(t *testing.T) {
	var r1, r2, r3 corev1.ResourceList
	r1 = make(corev1.ResourceList)
	r2 = make(corev1.ResourceList)
	r3 = make(corev1.ResourceList)

	c1 := resource.MustParse("20m")
	m1 := resource.MustParse("20Mi")
	d1 := resource.MustParse("5G")
	r1[corev1.ResourceCPU] = c1
	r1[corev1.ResourceMemory] = m1
	r1[corev1.ResourceStorage] = d1

	c2 := resource.MustParse("10m")
	m2 := resource.MustParse("25Mi")
	d2 := resource.MustParse("15G")

	r2[corev1.ResourceCPU] = c2
	r2[corev1.ResourceMemory] = m2
	r2[corev1.ResourceStorage] = d2

	c3 := resource.MustParse("250m")
	m3 := resource.MustParse("104857600")
	d3 := resource.MustParse("150Mi")

	r3[corev1.ResourceCPU] = c3
	r3[corev1.ResourceMemory] = m3
	r3[corev1.ResourceStorage] = d3

	resources := []corev1.ResourceList{r1, r2, r3}
	sumResources, err := scalehist.SumResources(resources)
	assert.Nil(t, err, "error summing resources")
	t.Logf("SumResources: %s of many resources", scalehist.ResourcesAsString(sumResources))
}

type podResources struct {
	SumRequests corev1.ResourceList
}

func TestLoadSaveResources(t *testing.T) {
	bytes, err := os.ReadFile("testdata/pod-blackbox-exporter.json")
	assert.Nil(t, err, "cant load pod json")
	var pod1 corev1.Pod
	err = json.Unmarshal(bytes, &pod1)
	assert.Nil(t, err, "can't unmarshall pod json into pod obj")
	allRequests := lo.Map(pod1.Spec.Containers, func(item corev1.Container, _ int) corev1.ResourceList {
		return item.Resources.Requests
	})
	summedPodRequests, err := scalehist.SumResources(allRequests)
	resourceStr := scalehist.ResourcesAsString(summedPodRequests)
	t.Logf("cont0 limits: %s\n", resourceStr)
	//assert.True(t, strings.Contains(resourceStr, "memory:23022Ki"), "resources should have memory with Ki")

	pr := podResources{SumRequests: summedPodRequests}
	bytes, err = json.Marshal(pr)
	path := "/tmp/pr.json"
	assert.Nil(t, err, "cant marshal podResources to json")
	err = os.WriteFile(path, bytes, 0755)
	assert.Nil(t, err, "cant write podResources json to file")
	t.Logf("wrote pod resources to json %s", path)
}
