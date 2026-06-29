package gpu

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func pendingGPUPod(gpus int64) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "train", Namespace: "ml"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "c", Resources: corev1.ResourceRequirements{Limits: gpuRequests(gpus)}}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}
}

func titles(r Result) string {
	var b strings.Builder
	for _, c := range r.Causes {
		b.WriteString(c.Title + "\n")
	}
	return b.String()
}

func has(r Result, substr string) bool { return strings.Contains(titles(r), substr) }

func TestDiagnose_NoNodeAdvertises(t *testing.T) {
	r := Diagnose(pendingGPUPod(1), nil, nil)
	if !has(r, "No node advertises") {
		t.Fatalf("want not-advertised blocker, got:\n%s", titles(r))
	}
	if !r.HasBlocker() {
		t.Fatal("want HasBlocker=true")
	}
}

func TestDiagnose_Fragmentation(t *testing.T) {
	// Two nodes, each with 1 GPU free; pod needs 2 — fits in aggregate, not on one.
	view := Cluster([]corev1.Node{gpuNode("g1", 4), gpuNode("g2", 4)}, []corev1.Pod{
		gpuPod("a", "g1", 3), gpuPod("b", "g2", 3),
	})
	r := Diagnose(pendingGPUPod(2), view, nil)
	if !has(r, "fragmentation") {
		t.Fatalf("want fragmentation blocker, got:\n%s", titles(r))
	}
}

func TestDiagnose_Insufficient(t *testing.T) {
	view := Cluster([]corev1.Node{gpuNode("g1", 4)}, []corev1.Pod{gpuPod("a", "g1", 4)})
	r := Diagnose(pendingGPUPod(1), view, nil)
	if !has(r, "Insufficient free") {
		t.Fatalf("want insufficient blocker, got:\n%s", titles(r))
	}
}

func TestDiagnose_UntoleratedGPUTaint(t *testing.T) {
	n := gpuNode("g1", 4)
	n.Spec.Taints = []corev1.Taint{{Key: "nvidia.com/gpu", Value: "present", Effect: corev1.TaintEffectNoSchedule}}
	view := Cluster([]corev1.Node{n}, nil)
	r := Diagnose(pendingGPUPod(1), view, nil)
	if !has(r, "untolerated GPU taints") {
		t.Fatalf("want taint blocker, got:\n%s", titles(r))
	}
}

func TestDiagnose_TolerationClearsTaint(t *testing.T) {
	n := gpuNode("g1", 4)
	n.Spec.Taints = []corev1.Taint{{Key: "nvidia.com/gpu", Value: "present", Effect: corev1.TaintEffectNoSchedule}}
	view := Cluster([]corev1.Node{n}, nil)
	pod := pendingGPUPod(1)
	pod.Spec.Tolerations = []corev1.Toleration{{Key: "nvidia.com/gpu", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}}
	r := Diagnose(pod, view, nil)
	if r.HasBlocker() {
		t.Fatalf("toleration should clear the taint, got:\n%s", titles(r))
	}
	if !has(r, "No GPU scheduling blocker") {
		t.Fatalf("want clean result, got:\n%s", titles(r))
	}
}

func TestDiagnose_NoGPURequest(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"}}
	r := Diagnose(pod, nil, nil)
	if r.HasBlocker() || !has(r, "no GPU resources") {
		t.Fatalf("want info-only no-GPU result, got:\n%s", titles(r))
	}
}

func TestDiagnose_FitsWhenRoomExists(t *testing.T) {
	view := Cluster([]corev1.Node{gpuNode("g1", 8)}, []corev1.Pod{gpuPod("a", "g1", 2)})
	r := Diagnose(pendingGPUPod(1), view, nil)
	if r.HasBlocker() {
		t.Fatalf("6 GPUs free, want no blocker, got:\n%s", titles(r))
	}
}
