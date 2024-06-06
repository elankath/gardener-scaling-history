package recorder

import (
	"fmt"
	"github.com/elankath/scalehist"
	assert "github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/yaml"
	"os"
	"testing"
)

func TestParseWorkerPools(t *testing.T) {
	data, err := os.ReadFile("worker.yaml")
	if err != nil {
		t.Fatalf("error reading the worker file %v", err)
		return
	}
	u := &unstructured.Unstructured{}
	err = yaml.Unmarshal(data, u)
	if err != nil {
		t.Fatalf("error unmarshalling %v", err)
		return
	}
	fmt.Println(u)
	_, err = parseWorkerPools(u)
	if err != nil {
		t.Fatalf("error parsing worker pools %v", err)
		return
	}
}

// pod triggered scale-up: [{shoot--i034796--aw2-p2-z1 1->3 (max: 3)}]
func TestParseTriggeredScaleUp(t *testing.T) {
	msg := "pod triggered scale-up: [{shoot--i034796--aw2-p2-z1 1->3 (max: 3)}]"
	ng, err := parseTriggeredScaleUpMessage(msg)
	assert.Nil(t, err)
	assert.Equal(t, ng.Name, "shoot--i034796--aw2-p2-z1")
	assert.Equal(t, ng.CurrentSize, 1)
	assert.Equal(t, ng.TargetSize, 3)
	assert.Equal(t, ng.MaxSize, 3)
	fmt.Printf("%v", ng)
}

func TestParseMachineSetScaleUp(t *testing.T) {
	msg := "Scaled up machine set shoot--i585976--target-gcp-p2-z2-7cbd6 to 1"
	expected := 1
	actual, err := parseMachineSetScaleUpMessage(msg)
	assert.Nil(t, err)
	assert.Equal(t, expected, actual)
}

func PrintPodMemory(t *testing.T, fileName string) {
	data, err := os.ReadFile(fileName)
	assert.Nil(t, err)
	var pod corev1.Pod
	err = json.Unmarshal(data, &pod)
	assert.Nil(t, err)
	memquant := pod.Spec.Containers[0].Resources.Requests.Memory()
	t.Logf("fileName: %s memory: %s", fileName, memquant)
	t.Logf("fileName: %s memoryscale: %s", fileName, memquant.Format)
	sumQuantity := scalehist.CumulatePodRequests(&pod)
	t.Logf("fileName: %s memory: %s", fileName, sumQuantity.Memory())
	t.Logf("fileName: %s memoryscale: %s", fileName, sumQuantity.Memory().Format)

	if sumQuantity.Memory().Format == resource.BinarySI {
		return
	}
	absVal, ok := sumQuantity.Memory().AsInt64()

	binaryMem1 := resource.NewQuantity(absVal*1000/1024, resource.BinarySI)
	assert.Equal(t, true, ok)

	binaryMem2, err := resource.ParseQuantity(fmt.Sprintf("%dKi", absVal/1024))
	assert.Nil(t, err)

	t.Logf("fileName: %s memory1: %s", fileName, binaryMem1)
	t.Logf("fileName: %s memoryscale1: %s", fileName, binaryMem1.Format)

	t.Logf("fileName: %s memory2: %s", fileName, &binaryMem2)
	t.Logf("fileName: %s memoryscale2: %s", fileName, binaryMem2.Format)
}
func TestPodMemory(t *testing.T) {
	PrintPodMemory(t, "/tmp/pod1.json")
	//PrintPodMemory(t, "/tmp/pod2.json")

}
