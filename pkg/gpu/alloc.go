package gpu

import (
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// ResourceAlloc is allocatable vs allocated for one GPU resource on one node.
type ResourceAlloc struct {
	Resource    string `json:"resource"`
	Allocatable int64  `json:"allocatable"`
	Allocated   int64  `json:"allocated"`
}

// Free is the unallocated count (never negative).
func (r ResourceAlloc) Free() int64 {
	if f := r.Allocatable - r.Allocated; f > 0 {
		return f
	}
	return 0
}

// NodeGPU is the GPU scheduling view of one node.
type NodeGPU struct {
	Name        string          `json:"name"`
	Ready       bool            `json:"ready"`
	Schedulable bool            `json:"schedulable"` // not cordoned
	GPUTaints   []corev1.Taint  `json:"gpuTaints,omitempty"`
	Resources   []ResourceAlloc `json:"resources"`
}

// Cluster computes the per-node GPU allocation view from nodes and the pods
// placed on them. Only nodes that advertise at least one GPU resource are
// returned; terminal pods (Succeeded/Failed) don't hold allocations.
func Cluster(nodes []corev1.Node, pods []corev1.Pod) []NodeGPU {
	allocated := map[string]map[corev1.ResourceName]int64{}
	for i := range pods {
		p := &pods[i]
		if p.Spec.NodeName == "" {
			continue
		}
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		req := PodGPURequests(p)
		if len(req) == 0 {
			continue
		}
		m := allocated[p.Spec.NodeName]
		if m == nil {
			m = map[corev1.ResourceName]int64{}
			allocated[p.Spec.NodeName] = m
		}
		for name, v := range req {
			m[name] += v
		}
	}

	var out []NodeGPU
	for i := range nodes {
		nd := &nodes[i]
		var res []ResourceAlloc
		for name, q := range nd.Status.Allocatable {
			if !IsGPUResource(name) {
				continue
			}
			capacity := q.Value()
			if capacity <= 0 {
				continue
			}
			res = append(res, ResourceAlloc{
				Resource:    string(name),
				Allocatable: capacity,
				Allocated:   allocated[nd.Name][name],
			})
		}
		if len(res) == 0 {
			continue
		}
		sort.Slice(res, func(i, j int) bool { return res[i].Resource < res[j].Resource })
		out = append(out, NodeGPU{
			Name:        nd.Name,
			Ready:       isReady(nd),
			Schedulable: !nd.Spec.Unschedulable,
			GPUTaints:   gpuTaints(nd),
			Resources:   res,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func isReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// gpuTaints returns the NoSchedule/NoExecute taints that look GPU-related —
// vendors commonly taint GPU nodes so only GPU workloads land there.
func gpuTaints(n *corev1.Node) []corev1.Taint {
	var out []corev1.Taint
	for _, t := range n.Spec.Taints {
		if t.Effect != corev1.TaintEffectNoSchedule && t.Effect != corev1.TaintEffectNoExecute {
			continue
		}
		lk := strings.ToLower(t.Key)
		if strings.Contains(lk, "gpu") || strings.Contains(lk, "nvidia") || strings.Contains(lk, "accelerator") {
			out = append(out, t)
		}
	}
	return out
}
