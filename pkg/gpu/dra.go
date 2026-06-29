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

// unallocatedClaim describes an unallocated claim. When DeviceClasses and
// ResourceSlices are supplied (non-nil), it pins the likely reason with concrete
// evidence: a referenced DeviceClass doesn't exist, no DRA driver is publishing
// devices, or devices exist but none are free/matching. With nil inputs it
// reports the generic unallocated state.
func unallocatedClaim(name string, fromTemplate bool, claim *resourcev1.ResourceClaim, slices []resourcev1.ResourceSlice, classes []resourcev1.DeviceClass) Cause {
	clsList := sortedClassKeys(requestedClasses(claim))
	classesStr := "(none)"
	if len(clsList) > 0 {
		classesStr = joinComma(clsList)
	}
	title := fmt.Sprintf("ResourceClaim %q is not allocated", name)
	detail := fmt.Sprintf("The scheduler couldn't allocate devices for this claim (requested DeviceClass(es): %s), so the pod stays Pending.", classesStr)
	fix := "Confirm the DRA driver for these classes is installed and publishing ResourceSlices (`kubectl get resourceslices`), that the DeviceClass exists, and that a matching device is free."

	// A referenced DeviceClass that doesn't exist can never match — definitive.
	if classes != nil {
		exists := map[string]bool{}
		for i := range classes {
			exists[classes[i].Name] = true
		}
		var missing []string
		for _, c := range clsList {
			if !exists[c] {
				missing = append(missing, c)
			}
		}
		if len(missing) > 0 {
			detail += fmt.Sprintf(" DeviceClass(es) not found in the cluster: %s — the request can never be satisfied.", joinComma(missing))
			fix = fmt.Sprintf("Create the missing DeviceClass(es) (%s) or reference an existing one in the claim's request.", joinComma(missing))
			return Cause{Severity: Blocker, Title: title, Detail: detail, Fix: fix}
		}
	}

	// Inventory + CEL selector matching: is any driver publishing devices, and
	// does any published device actually satisfy the request's selectors?
	if slices != nil {
		total := 0
		for i := range slices {
			total += len(slices[i].Spec.Devices)
		}
		if total == 0 {
			detail += " No ResourceSlices publish any devices — the DRA driver for these classes isn't running or hasn't published its inventory."
			fix = "Install/repair the DRA driver (e.g. the NVIDIA DRA driver) and confirm `kubectl get resourceslices` lists devices."
			return Cause{Severity: Blocker, Title: title, Detail: detail, Fix: fix}
		}

		classMap := map[string]*resourcev1.DeviceClass{}
		for i := range classes {
			classMap[classes[i].Name] = &classes[i]
		}
		var zeroMatch []string
		anyMatch := false
		compileErr := ""
		for ri := range claim.Spec.Devices.Requests {
			req := claim.Spec.Devices.Requests[ri].Exactly
			if req == nil {
				continue
			}
			exprs := requestSelectors(req, classMap)
			if len(exprs) == 0 {
				// No CEL selectors: any device of the class qualifies, so
				// membership isn't the blocker.
				anyMatch = true
				continue
			}
			if _, m, ce := matchDevices(exprs, slices); ce != "" {
				compileErr = ce
			} else if m == 0 {
				zeroMatch = append(zeroMatch, req.DeviceClassName)
			} else {
				anyMatch = true
			}
		}

		switch {
		case compileErr != "":
			detail += fmt.Sprintf(" %d device(s) are published, but a selector couldn't be evaluated (%s).", total, compileErr)
		case len(zeroMatch) > 0:
			detail += fmt.Sprintf(" Of %d published device(s), none satisfy the CEL selectors for request class(es) %s (evaluated with the scheduler's matcher) — the attribute/selector constraints exclude every device.", total, joinComma(dedupe(zeroMatch)))
			fix = "Loosen the request/DeviceClass CEL selectors, or add nodes whose devices expose the required attributes."
		case anyMatch:
			detail += fmt.Sprintf(" %d device(s) are published and at least one matches the request's selectors, but the claim is still unallocated — the matching devices are already in use.", total)
			fix = "Free a matching device (scale down / delete the claims holding it) or add nodes with more matching devices."
		default:
			detail += fmt.Sprintf(" %d device(s) are published cluster-wide, but none could be allocated.", total)
		}
	}

	return Cause{Severity: Blocker, Title: title, Detail: detail, Fix: fix}
}

func sortedClassKeys(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dedupe(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
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
