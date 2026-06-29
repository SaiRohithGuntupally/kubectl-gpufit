// Package gpu provides GPU scheduling/allocation diagnostics computed from the
// Kubernetes API alone — no DaemonSet, no metrics, no GPU hardware required.
//
// It deliberately stays out of the GPU *utilization* lane (covered by the
// gpu-top and gpugo plugins) and focuses on the scheduling questions the API can
// answer: which nodes advertise which accelerator resources, how many are
// already allocated, and why a given pod can't be placed.
package gpu

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// IsGPUResource reports whether an extended resource name denotes an accelerator
// (GPU) resource — NVIDIA full GPUs and MIG slices (e.g. "nvidia.com/mig-1g.10gb"),
// AMD/Intel GPUs, and shared/time-sliced variants. The match is intentionally
// broad: device plugins advertise vendor-scoped names that all share these hints.
func IsGPUResource(name corev1.ResourceName) bool {
	n := strings.ToLower(string(name))
	switch {
	case strings.HasPrefix(n, "nvidia.com/"):
		return true
	case strings.Contains(n, "gpu"):
		return true
	case strings.Contains(n, "mig-"):
		return true
	default:
		return false
	}
}

// containerGPU returns a container's GPU requests. Extended resources are
// requested via limits (requests default to the limit), so limits are
// authoritative; we fall back to requests when a limit is unset.
func containerGPU(c *corev1.Container) map[corev1.ResourceName]int64 {
	m := map[corev1.ResourceName]int64{}
	for name, q := range c.Resources.Limits {
		if IsGPUResource(name) {
			m[name] = q.Value()
		}
	}
	for name, q := range c.Resources.Requests {
		if IsGPUResource(name) {
			if _, ok := m[name]; !ok {
				m[name] = q.Value()
			}
		}
	}
	return m
}

// PodGPURequests sums a pod's effective GPU requests: the per-resource maximum of
// (sum of regular containers, largest single init container), matching how the
// scheduler computes a pod's resource footprint.
func PodGPURequests(pod *corev1.Pod) map[corev1.ResourceName]int64 {
	sum := map[corev1.ResourceName]int64{}
	for i := range pod.Spec.Containers {
		for name, v := range containerGPU(&pod.Spec.Containers[i]) {
			sum[name] += v
		}
	}
	for i := range pod.Spec.InitContainers {
		for name, v := range containerGPU(&pod.Spec.InitContainers[i]) {
			if v > sum[name] {
				sum[name] = v
			}
		}
	}
	return sum
}
