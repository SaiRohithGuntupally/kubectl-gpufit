package gpu

import (
	"strings"
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

func classObj(name string) resourcev1.DeviceClass {
	return resourcev1.DeviceClass{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func sliceObj(driver string, devices ...string) resourcev1.ResourceSlice {
	var ds []resourcev1.Device
	for _, d := range devices {
		ds = append(ds, resourcev1.Device{Name: d})
	}
	return resourcev1.ResourceSlice{
		ObjectMeta: metav1.ObjectMeta{Name: driver + "-slice"},
		Spec:       resourcev1.ResourceSliceSpec{Driver: driver, Devices: ds},
	}
}

func detailOf(r Result) string {
	s := ""
	for _, c := range r.Causes {
		s += c.Detail + "\n"
	}
	return s
}

func TestDiagnoseDRA_MissingDeviceClass(t *testing.T) {
	pod := draPod("train", "gpu", "claim-1")
	claims := []resourcev1.ResourceClaim{claimObj("claim-1", "gpu.nvidia.com", false)}
	// A different class exists; the requested one does not.
	classes := []resourcev1.DeviceClass{classObj("some.other.class")}
	r := DiagnoseDRA(pod, claims, []resourcev1.ResourceSlice{}, classes)
	if !contains(detailOf(r), "not found in the cluster") {
		t.Fatalf("want missing-DeviceClass detail, got:\n%s", detailOf(r))
	}
}

func TestDiagnoseDRA_NoDriverPublishing(t *testing.T) {
	pod := draPod("train", "gpu", "claim-1")
	claims := []resourcev1.ResourceClaim{claimObj("claim-1", "gpu.nvidia.com", false)}
	classes := []resourcev1.DeviceClass{classObj("gpu.nvidia.com")}
	r := DiagnoseDRA(pod, claims, []resourcev1.ResourceSlice{}, classes)
	if !contains(detailOf(r), "No ResourceSlices publish any devices") {
		t.Fatalf("want no-driver detail, got:\n%s", detailOf(r))
	}
}

func classWithSel(name, expr string) resourcev1.DeviceClass {
	return resourcev1.DeviceClass{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: resourcev1.DeviceClassSpec{
			Selectors: []resourcev1.DeviceSelector{{CEL: &resourcev1.CELDeviceSelector{Expression: expr}}},
		},
	}
}

func TestDiagnoseDRA_DevicesPublishedButNoneFree(t *testing.T) {
	// Class has no selectors → every published device qualifies, so the only
	// remaining explanation for an unallocated claim is "all in use".
	pod := draPod("train", "gpu", "claim-1")
	claims := []resourcev1.ResourceClaim{claimObj("claim-1", "gpu.nvidia.com", false)}
	classes := []resourcev1.DeviceClass{classObj("gpu.nvidia.com")}
	slices := []resourcev1.ResourceSlice{sliceObj("gpu.nvidia.com", "gpu-0", "gpu-1")}
	r := DiagnoseDRA(pod, claims, slices, classes)
	d := detailOf(r)
	if !contains(d, "2 device(s) are published") || !contains(d, "already in use") {
		t.Fatalf("want devices-in-use detail, got:\n%s", d)
	}
}

func TestDiagnoseDRA_CELNoDeviceMatches(t *testing.T) {
	// Class selector requires a driver that no published device has → 0 match.
	pod := draPod("train", "gpu", "claim-1")
	claims := []resourcev1.ResourceClaim{claimObj("claim-1", "gpu.nvidia.com", false)}
	classes := []resourcev1.DeviceClass{classWithSel("gpu.nvidia.com", `device.driver == "absent.example.com"`)}
	slices := []resourcev1.ResourceSlice{sliceObj("gpu.nvidia.com", "gpu-0", "gpu-1")}
	r := DiagnoseDRA(pod, claims, slices, classes)
	d := detailOf(r)
	if !contains(d, "none satisfy the CEL selectors") {
		t.Fatalf("want CEL no-match detail, got:\n%s", d)
	}
}

func TestDiagnoseDRA_CELMatchesButInUse(t *testing.T) {
	// Class selector matches the published driver → devices match, but claim is
	// still unallocated, so the reason is "in use", not "no match".
	pod := draPod("train", "gpu", "claim-1")
	claims := []resourcev1.ResourceClaim{claimObj("claim-1", "gpu.nvidia.com", false)}
	classes := []resourcev1.DeviceClass{classWithSel("gpu.nvidia.com", `device.driver == "gpu.nvidia.com"`)}
	slices := []resourcev1.ResourceSlice{sliceObj("gpu.nvidia.com", "gpu-0")}
	r := DiagnoseDRA(pod, claims, slices, classes)
	d := detailOf(r)
	if !contains(d, "already in use") {
		t.Fatalf("want matches-but-in-use detail, got:\n%s", d)
	}
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

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
