package gpu

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func compPod(name, node string, ready bool, waitingReason string) corev1.Pod {
	cs := corev1.ContainerStatus{Name: "c", Ready: ready}
	phase := corev1.PodRunning
	if waitingReason != "" {
		cs.State.Waiting = &corev1.ContainerStateWaiting{Reason: waitingReason}
		cs.Ready = false
	}
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "gpu-operator"},
		Spec:       corev1.PodSpec{NodeName: node},
		Status:     corev1.PodStatus{Phase: phase, ContainerStatuses: []corev1.ContainerStatus{cs}},
	}
}

func TestAnalyzeOperatorChain_NotDetected(t *testing.T) {
	cs := AnalyzeOperatorChain([]corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Name: "some-app", Namespace: "default"}},
	})
	if cs.Detected {
		t.Fatalf("expected not detected, got %+v", cs)
	}
}

func TestAnalyzeOperatorChain_FirstBrokenInOrder(t *testing.T) {
	// Driver crashlooping AND device-plugin pending: the driver is earlier in the
	// chain, so it must be reported as the first broken link.
	pods := []corev1.Pod{
		compPod("nvidia-driver-daemonset-abc", "gpu-1", false, "CrashLoopBackOff"),
		compPod("nvidia-device-plugin-daemonset-xyz", "gpu-1", false, "CreateContainerError"),
		compPod("gpu-feature-discovery-1", "gpu-1", true, ""),
	}
	cs := AnalyzeOperatorChain(pods)
	if !cs.Detected || cs.FirstBroken == nil {
		t.Fatalf("expected detected + a broken link, got %+v", cs)
	}
	if cs.FirstBroken.Name != "driver" {
		t.Errorf("first broken should be the driver, got %q", cs.FirstBroken.Name)
	}
	if !strings.Contains(cs.FirstBroken.Reason, "CrashLoopBackOff") {
		t.Errorf("reason should mention CrashLoopBackOff, got %q", cs.FirstBroken.Reason)
	}
}

func TestAnalyzeOperatorChain_AllHealthy(t *testing.T) {
	cs := AnalyzeOperatorChain([]corev1.Pod{
		compPod("nvidia-device-plugin-daemonset-xyz", "gpu-1", true, ""),
	})
	if !cs.Detected || cs.FirstBroken != nil {
		t.Fatalf("expected detected + no broken link, got %+v", cs)
	}
}

func TestDiagnose_NotAdvertised_NamesBrokenComponent(t *testing.T) {
	chain := AnalyzeOperatorChain([]corev1.Pod{
		compPod("nvidia-device-plugin-daemonset-xyz", "gpu-2", false, "CrashLoopBackOff"),
	})
	r := Diagnose(pendingGPUPod(1), nil, &chain)
	if !r.HasBlocker() {
		t.Fatal("want a blocker")
	}
	if !has(r, "device-plugin") {
		t.Fatalf("want device-plugin named in the cause, got:\n%s", titles(r))
	}
	// The fix should point at the actual broken pod.
	if !strings.Contains(r.Causes[0].Fix, "nvidia-device-plugin-daemonset-xyz") {
		t.Errorf("fix should reference the broken pod, got: %q", r.Causes[0].Fix)
	}
}

func TestDiagnose_NotAdvertised_NoStackFound(t *testing.T) {
	chain := AnalyzeOperatorChain([]corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: "default"}},
	})
	r := Diagnose(pendingGPUPod(1), nil, &chain)
	if !has(r, "No GPU device plugin found") {
		t.Fatalf("want no-device-plugin cause, got:\n%s", titles(r))
	}
}
