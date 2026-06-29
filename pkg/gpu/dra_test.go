package gpu

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func strptr(s string) *string { return &s }

func draPod(name, podClaimName, claimName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ml"},
		Spec: corev1.PodSpec{
			ResourceClaims: []corev1.PodResourceClaim{
				{Name: podClaimName, ResourceClaimName: strptr(claimName)},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}
}

func claimObj(name, deviceClass string, allocated bool) resourcev1.ResourceClaim {
	c := resourcev1.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ml"},
		Spec: resourcev1.ResourceClaimSpec{
			Devices: resourcev1.DeviceClaim{
				Requests: []resourcev1.DeviceRequest{
					{Name: "gpu", Exactly: &resourcev1.ExactDeviceRequest{DeviceClassName: deviceClass, Count: 1}},
				},
			},
		},
	}
	if allocated {
		c.Status.Allocation = &resourcev1.AllocationResult{}
	}
	return c
}

func TestUsesDRA(t *testing.T) {
	if UsesDRA(&corev1.Pod{}) {
		t.Error("plain pod should not use DRA")
	}
	if !UsesDRA(draPod("p", "gpu", "claim-1")) {
		t.Error("pod with resourceClaims should use DRA")
	}
}

func TestDiagnoseDRA_Unallocated(t *testing.T) {
	pod := draPod("train", "gpu", "claim-1")
	claims := []resourcev1.ResourceClaim{claimObj("claim-1", "gpu.nvidia.com", false)}
	r := DiagnoseDRA(pod, claims, nil, nil)
	if !r.HasBlocker() {
		t.Fatalf("unallocated claim should block, got:\n%s", titles(r))
	}
	if !has(r, "not allocated") {
		t.Fatalf("want 'not allocated' cause, got:\n%s", titles(r))
	}
	if r.Requests["gpu.nvidia.com"] != 1 {
		t.Errorf("want device class recorded in requests, got %v", r.Requests)
	}
}

func TestDiagnoseDRA_Allocated(t *testing.T) {
	pod := draPod("train", "gpu", "claim-1")
	claims := []resourcev1.ResourceClaim{claimObj("claim-1", "gpu.nvidia.com", true)}
	r := DiagnoseDRA(pod, claims, nil, nil)
	if r.HasBlocker() {
		t.Fatalf("allocated claim should not block, got:\n%s", titles(r))
	}
	if !has(r, "is allocated") {
		t.Fatalf("want allocated info, got:\n%s", titles(r))
	}
}

func TestDiagnoseDRA_ClaimMissing(t *testing.T) {
	pod := draPod("train", "gpu", "claim-1")
	r := DiagnoseDRA(pod, nil, nil, nil)
	if !has(r, "not found") {
		t.Fatalf("want claim-not-found cause, got:\n%s", titles(r))
	}
}

func TestDiagnoseDRA_TemplateNotMaterialized(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "train", Namespace: "ml"},
		Spec: corev1.PodSpec{
			ResourceClaims: []corev1.PodResourceClaim{
				{Name: "gpu", ResourceClaimTemplateName: strptr("gpu-template")},
			},
		},
	}
	r := DiagnoseDRA(pod, nil, nil, nil)
	if !has(r, "not created yet") {
		t.Fatalf("want template-not-materialized cause, got:\n%s", titles(r))
	}
}
