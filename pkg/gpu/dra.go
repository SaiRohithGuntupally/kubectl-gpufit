package gpu

import (
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
)

// UsesDRA reports whether the pod requests devices via Dynamic Resource
// Allocation (k8s 1.34+) — through pod.spec.resourceClaims rather than
// nvidia.com/gpu-style extended resources. Such pods are invisible to the
// extended-resource path, so the caller must route them here.
func UsesDRA(pod *corev1.Pod) bool {
	return len(pod.Spec.ResourceClaims) > 0
}

// claimRef resolves a pod's resourceClaims[] entry to the actual ResourceClaim
// object name. Direct references use ResourceClaimName; template-generated claims
// are resolved via pod.status.resourceClaimStatuses.
func claimRef(pod *corev1.Pod, prc corev1.PodResourceClaim) (name string, fromTemplate bool) {
	if prc.ResourceClaimName != nil && *prc.ResourceClaimName != "" {
		return *prc.ResourceClaimName, false
	}
	for _, s := range pod.Status.ResourceClaimStatuses {
		if s.Name == prc.Name && s.ResourceClaimName != nil {
			return *s.ResourceClaimName, true
		}
	}
	return "", prc.ResourceClaimTemplateName != nil
}

// requestedClasses returns the DeviceClass names a claim requests, with counts.
func requestedClasses(claim *resourcev1.ResourceClaim) map[string]int64 {
	out := map[string]int64{}
	for _, r := range claim.Spec.Devices.Requests {
		if r.Exactly == nil {
			continue
		}
		n := int64(1)
		if r.Exactly.Count > 0 {
			out[r.Exactly.DeviceClassName] += r.Exactly.Count
		} else {
			out[r.Exactly.DeviceClassName] += n
		}
	}
	return out
}

// DiagnoseDRA explains why a DRA pod's device claims aren't satisfied. Phase 1
// (this version) resolves each pod claim to its ResourceClaim object and reports
// allocation status: not-yet-created, unallocated (the scheduler couldn't place
// it), or allocated. slices/classes enable the deeper "no matching device"
// analysis (added in a later phase); pass nil to skip it.
func DiagnoseDRA(pod *corev1.Pod, claims []resourcev1.ResourceClaim, slices []resourcev1.ResourceSlice, classes []resourcev1.DeviceClass) Result {
	res := Result{Namespace: pod.Namespace, Pod: pod.Name, Requests: map[string]int64{}}

	byName := map[string]*resourcev1.ResourceClaim{}
	for i := range claims {
		byName[claims[i].Name] = &claims[i]
	}

	for _, prc := range pod.Spec.ResourceClaims {
		name, fromTemplate := claimRef(pod, prc)

		if name == "" {
			res.Causes = append(res.Causes, Cause{
				Severity: Blocker,
				Title:    fmt.Sprintf("ResourceClaim for %q not created yet", prc.Name),
				Detail:   "The pod references a ResourceClaimTemplate, but the control plane hasn't materialized a ResourceClaim for it yet (or you lack RBAC to read it).",
				Fix:      "Check the resource-claim controller and `kubectl get resourceclaims`; ensure the ResourceClaimTemplate exists in this namespace.",
			})
			continue
		}

		claim, ok := byName[name]
		if !ok {
			res.Causes = append(res.Causes, Cause{
				Severity: Blocker,
				Title:    fmt.Sprintf("ResourceClaim %q not found", name),
				Detail:   fmt.Sprintf("Pod claim %q resolves to ResourceClaim %q, which doesn't exist in namespace %q.", prc.Name, name, pod.Namespace),
				Fix:      "Create the ResourceClaim, or fix the reference in the pod spec.",
			})
			continue
		}

		for cls, n := range requestedClasses(claim) {
			res.Requests[cls] += n
		}

		if claim.Status.Allocation != nil {
			res.Causes = append(res.Causes, Cause{
				Severity: Info,
				Title:    fmt.Sprintf("ResourceClaim %q is allocated", name),
				Detail:   "Its devices are allocated, so DRA isn't the blocker for this claim.",
				Fix:      "If the pod is still Pending, check non-DRA causes (`kubectl why-pending`) or the scheduler event.",
			})
			continue
		}

		c := unallocatedClaim(name, fromTemplate, claim, slices, classes)
		res.Causes = append(res.Causes, c)
	}

	if len(res.Causes) == 0 {
		res.Causes = append(res.Causes, Cause{
			Severity: Info,
			Title:    "Pod uses DRA but declares no resource claims to analyze",
			Detail:   "No pod.spec.resourceClaims entries resolved to a request.",
			Fix:      "Verify the pod's resourceClaims and container claim references.",
		})
	}
	return res
}

// unallocatedClaim describes an unallocated claim. With slices/classes it points
// at the likely reason (no node publishes a matching device); without them it
// reports the unallocated state and the classes involved.
func unallocatedClaim(name string, fromTemplate bool, claim *resourcev1.ResourceClaim, slices []resourcev1.ResourceSlice, classes []resourcev1.DeviceClass) Cause {
	clsList := sortedClassKeys(requestedClasses(claim))
	classesStr := "(none)"
	if len(clsList) > 0 {
		classesStr = joinComma(clsList)
	}
	return Cause{
		Severity: Blocker,
		Title:    fmt.Sprintf("ResourceClaim %q is not allocated", name),
		Detail: fmt.Sprintf(
			"The scheduler hasn't been able to allocate devices for this claim (requested DeviceClass(es): %s), so the pod stays Pending. Common DRA causes: no node publishes a ResourceSlice with a device matching the class/selectors, the DRA driver isn't running, or all matching devices are already allocated.",
			classesStr),
		Fix: "Confirm the DRA driver for these classes is installed and publishing ResourceSlices (`kubectl get resourceslices`), that a DeviceClass named above exists, and that a node actually has a matching device free.",
	}
}

func sortedClassKeys(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func joinComma(s []string) string {
	res := ""
	for i, v := range s {
		if i > 0 {
			res += ", "
		}
		res += v
	}
	return res
}
