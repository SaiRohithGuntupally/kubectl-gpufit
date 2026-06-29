package gpu

import (
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// chainStep is one link in the GPU enablement dependency chain, in the order a
// node must traverse before it can advertise GPUs. Components are matched by
// case-insensitive substring against pod names across all namespaces, which
// covers the NVIDIA GPU Operator, standalone device-plugin DaemonSets, and the
// cloud-managed device plugins (GKE/EKS/AKS). It is intentionally best-effort:
// when nothing matches we report "not detected" rather than guess.
type chainStep struct {
	Label    string
	Patterns []string
}

// gpuChain lists the components in dependency order. A break early in the chain
// keeps everything downstream — including the device plugin that advertises the
// resource — from working.
var gpuChain = []chainStep{
	{"node-feature-discovery", []string{"node-feature-discovery", "nfd-worker", "nfd-master"}},
	{"driver", []string{"nvidia-driver", "nvidia-vgpu-manager"}},
	{"container-toolkit", []string{"nvidia-container-toolkit"}},
	{"device-plugin", []string{"nvidia-device-plugin", "gpu-device-plugin", "k8s-device-plugin", "amdgpu-device-plugin"}},
	{"gpu-feature-discovery", []string{"gpu-feature-discovery", "nvidia-gfd"}},
	{"dcgm", []string{"nvidia-dcgm"}},
	{"mig-manager", []string{"nvidia-mig-manager"}},
	{"operator-validator", []string{"nvidia-operator-validator"}},
}

// Component is the observed health of one GPU stack component.
type Component struct {
	Name      string `json:"name"`
	Pod       string `json:"pod,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Node      string `json:"node,omitempty"`
	Healthy   bool   `json:"healthy"`
	Reason    string `json:"reason,omitempty"`
}

// ChainStatus is the result of inspecting the GPU enablement chain.
type ChainStatus struct {
	Detected    bool        `json:"detected"`
	Components  []Component `json:"components,omitempty"`
	FirstBroken *Component  `json:"firstBroken,omitempty"`
}

// AnalyzeOperatorChain inspects pods cluster-wide and reports the health of each
// GPU stack component it can find, plus the first broken link in dependency
// order. It reads pod status only — no agent, no exec.
func AnalyzeOperatorChain(pods []corev1.Pod) ChainStatus {
	var cs ChainStatus
	for _, step := range gpuChain {
		matches := matchPods(pods, step.Patterns)
		if len(matches) == 0 {
			continue // not present — don't fabricate a component we didn't observe
		}
		cs.Detected = true
		comp := Component{Name: step.Label, Healthy: true, Pod: matches[0].Name, Namespace: matches[0].Namespace}
		for i := range matches {
			if ok, reason := podHealth(&matches[i]); !ok {
				comp.Healthy = false
				comp.Reason = reason
				comp.Pod = matches[i].Name
				comp.Namespace = matches[i].Namespace
				comp.Node = matches[i].Spec.NodeName
				break
			}
		}
		cs.Components = append(cs.Components, comp)
		if !comp.Healthy && cs.FirstBroken == nil {
			c := comp
			cs.FirstBroken = &c
		}
	}
	return cs
}

func matchPods(pods []corev1.Pod, patterns []string) []corev1.Pod {
	var out []corev1.Pod
	for i := range pods {
		name := strings.ToLower(pods[i].Name)
		for _, pat := range patterns {
			if strings.Contains(name, pat) {
				out = append(out, pods[i])
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// podHealth reports whether a component pod is healthy, and if not, a short
// reason. Succeeded pods (e.g. one-shot validators) count as healthy.
func podHealth(p *corev1.Pod) (bool, string) {
	switch p.Status.Phase {
	case corev1.PodSucceeded:
		return true, ""
	case corev1.PodFailed:
		return false, fmt.Sprintf("pod Failed (%s)", podLoc(p))
	}
	for _, cs := range p.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil {
			if w.Reason != "" && w.Reason != "ContainerCreating" && w.Reason != "PodInitializing" {
				return false, fmt.Sprintf("%s (%s)", w.Reason, podLoc(p))
			}
		}
		if t := cs.State.Terminated; t != nil && t.ExitCode != 0 {
			return false, fmt.Sprintf("%s exit %d (%s)", t.Reason, t.ExitCode, podLoc(p))
		}
	}
	if p.Status.Phase != corev1.PodRunning {
		return false, fmt.Sprintf("%s (%s)", p.Status.Phase, podLoc(p))
	}
	for _, cs := range p.Status.ContainerStatuses {
		if !cs.Ready {
			return false, fmt.Sprintf("container %q not Ready (%s)", cs.Name, podLoc(p))
		}
	}
	return true, ""
}

func podLoc(p *corev1.Pod) string {
	if p.Spec.NodeName != "" {
		return "on " + p.Spec.NodeName
	}
	return p.Namespace + "/" + p.Name
}

// notAdvertised builds the cause for "the requested GPU resource is on no node",
// enriched with the operator-chain status when available so it names the exact
// broken component instead of a generic checklist.
func notAdvertised(name string, need int64, chain *ChainStatus) Cause {
	base := fmt.Sprintf("The pod requests %d of %q, but no Ready, schedulable node advertises it.", need, name)

	switch {
	case chain != nil && chain.FirstBroken != nil:
		fb := chain.FirstBroken
		return Cause{
			Severity: Blocker,
			Title:    fmt.Sprintf("GPU stack broken at the %s — nothing advertises %s", fb.Name, name),
			Detail: base + fmt.Sprintf(
				" The GPU enablement chain is broken at the %s: %s. Everything downstream — including the device plugin that advertises %s — stays down until this is fixed.",
				fb.Name, fb.Reason, name),
			Fix: fmt.Sprintf(
				"Fix the %s first: `kubectl -n %s describe pod %s` and check its logs. Chain order: NFD → driver → container-toolkit → device-plugin → GFD → DCGM → MIG-manager.",
				fb.Name, fb.Namespace, fb.Pod),
		}
	case chain != nil && !chain.Detected:
		return Cause{
			Severity: Blocker,
			Title:    fmt.Sprintf("No GPU device plugin found — nothing advertises %s", name),
			Detail:   base + " No GPU device-plugin or GPU Operator pods were found in the cluster, so no node will ever advertise this resource.",
			Fix:      "Install the NVIDIA GPU Operator (or your cloud's GPU device plugin / a standalone nvidia-device-plugin DaemonSet), then confirm GPU nodes report the resource.",
		}
	default:
		return Cause{
			Severity: Blocker,
			Title:    fmt.Sprintf("No node advertises %s", name),
			Detail: base + " The GPU stack components found look healthy, so the likely causes are: no node has this hardware or MIG profile, the resource name is misspelled, or GPU Feature Discovery hasn't labeled the nodes yet.",
			Fix: fmt.Sprintf(
				"Confirm a node actually provides %q (right hardware / MIG profile) and that GPU Feature Discovery has labeled it; verify the resource name matches what the device plugin advertises.",
				name),
		}
	}
}
