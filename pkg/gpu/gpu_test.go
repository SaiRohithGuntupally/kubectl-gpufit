package gpu

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIsGPUResource(t *testing.T) {
	cases := map[corev1.ResourceName]bool{
		"nvidia.com/gpu":          true,
		"nvidia.com/mig-1g.10gb":  true,
		"nvidia.com/gpu.shared":   true,
		"amd.com/gpu":             true,
		"gpu.intel.com/i915":      true,
		"cpu":                     false,
		"memory":                  false,
		"ephemeral-storage":       false,
		"example.com/foo":         false,
		"hugepages-2Mi":           false,
	}
	for name, want := range cases {
		if got := IsGPUResource(name); got != want {
			t.Errorf("IsGPUResource(%q) = %v, want %v", name, got, want)
		}
	}
}

func gpuRequests(gpus int64) corev1.ResourceList {
	return corev1.ResourceList{"nvidia.com/gpu": *resource.NewQuantity(gpus, resource.DecimalSI)}
}

func TestPodGPURequests_SumsContainersAndMaxesInit(t *testing.T) {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "a", Resources: corev1.ResourceRequirements{Limits: gpuRequests(1)}},
				{Name: "b", Resources: corev1.ResourceRequirements{Limits: gpuRequests(2)}},
			},
			InitContainers: []corev1.Container{
				{Name: "init", Resources: corev1.ResourceRequirements{Limits: gpuRequests(2)}},
			},
		},
	}
	got := PodGPURequests(p)
	if got["nvidia.com/gpu"] != 3 {
		t.Errorf("expected 3 (sum of containers > init max), got %d", got["nvidia.com/gpu"])
	}
}

func TestPodGPURequests_NoGPU(t *testing.T) {
	p := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Name: "a", Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{
			"cpu": resource.MustParse("500m"),
		}},
	}}}}
	if got := PodGPURequests(p); len(got) != 0 {
		t.Errorf("expected no GPU requests, got %v", got)
	}
}
