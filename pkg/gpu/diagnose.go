package gpu

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// Severity ranks how strongly a cause blocks GPU scheduling.
type Severity int

const (
	Blocker Severity = iota // almost certainly why it's stuck
	Warning                 // contributes, or filters out some nodes
	Info                    // context, not necessarily blocking
)

func (s Severity) Icon() string {
	switch s {
	case Blocker:
		return "✗"
	case Warning:
		return "!"
	default:
		return "ℹ"
	}
}

func (s Severity) String() string {
	switch s {
	case Blocker:
		return "blocker"
	case Warning:
		return "warning"
	default:
		return "info"
	}
}

// MarshalJSON emits the severity as its name rather than an opaque integer.
func (s Severity) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(s.String())), nil
}

// Cause is a single human-readable finding with a suggested fix.
type Cause struct {
	Severity Severity `json:"severity"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	Fix      string   `json:"fix"`
}

// NodeCandidate is a node where the pod's GPU requests would fit right now.
type NodeCandidate struct {
	Node string           `json:"node"`
	Free map[string]int64 `json:"free"` // free count per requested GPU resource
}

// Result is the GPU diagnosis for one pod.
type Result struct {
	Namespace  string           `json:"namespace"`
	Pod        string           `json:"pod"`
	Requests   map[string]int64 `json:"requests"`
	Causes     []Cause          `json:"causes"`
	Candidates []NodeCandidate  `json:"candidates,omitempty"`
}

// HasBlocker reports whether any cause is a Blocker (used for the CLI exit code).
func (r Result) HasBlocker() bool {
	for _, c := range r.Causes {
		if c.Severity == Blocker {
			return true
		}
	}
	return false
}

// Diagnose explains why a GPU pod cannot be scheduled, using the cluster's GPU
// allocation view. It covers the API-visible causes: the requested resource
// isn't advertised by any node (device plugin / hardware / MIG-profile mismatch),
// fragmentation across nodes, insufficient free GPUs, and untolerated GPU taints.
//
// chain, if non-nil, is the GPU enablement dependency-chain status; it lets the
// "no node advertises" finding point at the exact broken component. Pass nil to
// skip that enrichment.
func Diagnose(pod *corev1.Pod, nodes []NodeGPU, chain *ChainStatus) Result {
	req := PodGPURequests(pod)
	res := Result{Namespace: pod.Namespace, Pod: pod.Name, Requests: map[string]int64{}}
	for k, v := range req {
		res.Requests[string(k)] = v
	}

	if len(req) == 0 {
		res.Causes = append(res.Causes, Cause{
			Severity: Info,
			Title:    "Pod requests no GPU resources",
			Detail:   "This pod has no GPU/accelerator resource requests, so GPU scheduling isn't the blocker.",
			Fix:      "If it's stuck Pending for non-GPU reasons, use `kubectl why-pending` or `kubectl describe pod`.",
		})
		return res
	}

	var usable []NodeGPU
	for _, n := range nodes {
		if n.Ready && n.Schedulable {
			usable = append(usable, n)
		}
	}

	res.Candidates = computeCandidates(pod, req, usable)

	for _, name := range sortedKeys(req) {
		need := req[corev1.ResourceName(name)]
		var advertisers int
		var maxFree, sumFree, maxFreeUntainted int64
		untoleratedTaints := map[string]bool{}

		for _, n := range usable {
			ra, ok := resourceOf(n, name)
			if !ok {
				continue
			}
			advertisers++
			free := ra.Free()
			sumFree += free
			if free > maxFree {
				maxFree = free
			}
			bad := untolerated(pod, n.GPUTaints)
			if len(bad) == 0 {
				if free > maxFreeUntainted {
					maxFreeUntainted = free
				}
			} else {
				for _, t := range bad {
					untoleratedTaints[taintStr(t)] = true
				}
			}
		}

		switch {
		case advertisers == 0:
			res.Causes = append(res.Causes, notAdvertised(name, need, chain))
		case need > maxFree && need <= sumFree && advertisers > 1:
			res.Causes = append(res.Causes, Cause{
				Severity: Blocker,
				Title:    fmt.Sprintf("GPU fragmentation — %s exists, but not on one node", name),
				Detail: fmt.Sprintf(
					"Pod needs %d of %q on a single node. No node has that many free, though %d are free across %d node(s) in aggregate (largest single node free: %d).",
					need, name, sumFree, advertisers, maxFree),
				Fix: "Consolidate GPU workloads to free a contiguous set on one node, lower the pod's GPU count, or add a node with enough GPUs. Kubernetes cannot split one GPU request across nodes.",
			})
		case need > maxFree:
			res.Causes = append(res.Causes, Cause{
				Severity: Blocker,
				Title:    fmt.Sprintf("Insufficient free %s", name),
				Detail: fmt.Sprintf(
					"Pod needs %d of %q, but the most any node has free is %d (%d free across %d node(s)). Kubernetes allocates whole GPUs — a job using a fraction of a GPU still consumes a full unit unless you use MIG or time-slicing.",
					need, name, maxFree, sumFree, advertisers),
				Fix: "Free GPUs (scale down/evict GPU pods), add GPU nodes, or adopt MIG / time-slicing to pack more workloads per GPU.",
			})
		case need > maxFreeUntainted && len(untoleratedTaints) > 0:
			res.Causes = append(res.Causes, Cause{
				Severity: Blocker,
				Title:    fmt.Sprintf("Free %s only on nodes with untolerated GPU taints", name),
				Detail: "Enough free GPUs exist, but every node that could host the pod carries a GPU taint it doesn't tolerate: " +
					strings.Join(keys(untoleratedTaints), ", ") + ".",
				Fix: "Add a matching toleration to the pod spec — GPU nodes are commonly tainted so only GPU workloads land there.",
			})
		}
	}

	if len(res.Causes) == 0 {
		res.Causes = append(res.Causes, Cause{
			Severity: Info,
			Title:    "No GPU scheduling blocker found",
			Detail:   "The GPU resources this pod requests appear satisfiable on at least one Ready, schedulable, untolerated-taint-free node. If it's still Pending, the cause is likely non-GPU (CPU/memory, affinity) or dynamic (priority/preemption).",
			Fix:      "Check `kubectl why-pending`, or the scheduler's FailedScheduling event via `kubectl describe pod`.",
		})
	}
	return res
}

// computeCandidates returns the usable nodes where every requested GPU resource
// fits right now and the pod tolerates the node's GPU taints.
func computeCandidates(pod *corev1.Pod, req map[corev1.ResourceName]int64, usable []NodeGPU) []NodeCandidate {
	var out []NodeCandidate
	for _, n := range usable {
		if len(untolerated(pod, n.GPUTaints)) > 0 {
			continue
		}
		free := map[string]int64{}
		fits := true
		for name, need := range req {
			ra, ok := resourceOf(n, string(name))
			if !ok || ra.Free() < need {
				fits = false
				break
			}
			free[string(name)] = ra.Free()
		}
		if fits {
			out = append(out, NodeCandidate{Node: n.Name, Free: free})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out
}

func resourceOf(n NodeGPU, name string) (ResourceAlloc, bool) {
	for _, r := range n.Resources {
		if r.Resource == name {
			return r, true
		}
	}
	return ResourceAlloc{}, false
}

func untolerated(pod *corev1.Pod, taints []corev1.Taint) []corev1.Taint {
	var bad []corev1.Taint
	for _, t := range taints {
		if !tolerates(pod.Spec.Tolerations, t) {
			bad = append(bad, t)
		}
	}
	return bad
}

func tolerates(tols []corev1.Toleration, t corev1.Taint) bool {
	for i := range tols {
		if tolerationMatches(tols[i], t) {
			return true
		}
	}
	return false
}

// tolerationMatches implements the standard taint/toleration matching rules:
// an empty effect/key wildcards that dimension, Exists ignores the value, and an
// empty operator defaults to Equal.
func tolerationMatches(tol corev1.Toleration, t corev1.Taint) bool {
	if tol.Effect != "" && tol.Effect != t.Effect {
		return false
	}
	if tol.Key != "" && tol.Key != t.Key {
		return false
	}
	switch tol.Operator {
	case corev1.TolerationOpExists:
		return true
	case corev1.TolerationOpEqual, "":
		return tol.Value == t.Value
	default:
		return false
	}
}

func taintStr(t corev1.Taint) string {
	return fmt.Sprintf("%s=%s:%s", t.Key, t.Value, t.Effect)
}

func sortedKeys(m map[corev1.ResourceName]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, string(k))
	}
	sort.Strings(out)
	return out
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
