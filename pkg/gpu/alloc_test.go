package gpu

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func gpuNode(name string, gpus int64) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{"nvidia.com/gpu": *resource.NewQuantity(gpus, resource.DecimalSI)},
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
}

func gpuPod(name, node string, gpus int64) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName:   node,
			Containers: []corev1.Container{{Name: "c", Resources: corev1.ResourceRequirements{Limits: gpuRequests(gpus)}}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func TestCluster_AllocationMath(t *testing.T) {
	nodes := []corev1.Node{gpuNode("g1", 8), gpuNode("g2", 4)}
	cpuOnly := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "cpu1"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{"cpu": resource.MustParse("8")},
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	nodes = append(nodes, cpuOnly)

	pods := []corev1.Pod{
		gpuPod("a", "g1", 6),
		gpuPod("b", "g2", 1),
	}
	view := Cluster(nodes, pods)

	if len(view) != 2 {
		t.Fatalf("expected 2 GPU nodes (cpu-only excluded), got %d", len(view))
	}
	byName := map[string]ResourceAlloc{}
	for _, n := range view {
		byName[n.Name] = n.Resources[0]
	}
	if g := byName["g1"]; g.Allocated != 6 || g.Free() != 2 {
		t.Errorf("g1: allocated=%d free=%d, want 6/2", g.Allocated, g.Free())
	}
	if g := byName["g2"]; g.Allocated != 1 || g.Free() != 3 {
		t.Errorf("g2: allocated=%d free=%d, want 1/3", g.Allocated, g.Free())
	}
}

func TestCluster_TerminalPodsDontHoldGPUs(t *testing.T) {
	nodes := []corev1.Node{gpuNode("g1", 4)}
	done := gpuPod("done", "g1", 4)
	done.Status.Phase = corev1.PodSucceeded
	view := Cluster(nodes, []corev1.Pod{done})
	if view[0].Resources[0].Allocated != 0 {
		t.Errorf("succeeded pod should not hold GPUs, got allocated=%d", view[0].Resources[0].Allocated)
	}
}
