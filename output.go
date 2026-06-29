package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"sigs.k8s.io/yaml"

	"github.com/SaiRohithGuntupally/kubectl-gpufit/pkg/gpu"
)

// structured reports whether the output format is machine-readable.
func structured(format string) bool {
	return format == "json" || format == "yaml"
}

// emit writes v as JSON or YAML.
func emit(format string, v any) error {
	switch format {
	case "json":
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
	case "yaml":
		b, err := yaml.Marshal(v)
		if err != nil {
			return err
		}
		fmt.Print(string(b))
	default:
		return fmt.Errorf("unknown output format %q", format)
	}
	return nil
}

// printAlloc renders the per-node GPU allocation table.
func printAlloc(view []gpu.NodeGPU) {
	if len(view) == 0 {
		fmt.Println("No GPU resources found on any node. 🤷")
		fmt.Println("(No node advertises nvidia.com/gpu, MIG, or other accelerator resources — check the device plugin / GPU Operator.)")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "NODE\tGPU RESOURCE\tALLOCATABLE\tALLOCATED\tFREE\tSTATUS")
	for _, n := range view {
		status := nodeStatus(n)
		for _, r := range n.Resources {
			fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t%s\n",
				n.Name, r.Resource, r.Allocatable, r.Allocated, r.Free(), status)
		}
	}
	w.Flush()
}

func nodeStatus(n gpu.NodeGPU) string {
	switch {
	case !n.Ready:
		return "NotReady"
	case !n.Schedulable:
		return "cordoned"
	case len(n.GPUTaints) > 0:
		return "tainted"
	default:
		return "Ready"
	}
}

// printWhy renders a per-pod GPU diagnosis.
func printWhy(r gpu.Result) {
	fmt.Println()
	reqs := make([]string, 0, len(r.Requests))
	for k, v := range r.Requests {
		reqs = append(reqs, fmt.Sprintf("%d %s", v, k))
	}
	sort.Strings(reqs)
	rs := "no GPU"
	if len(reqs) > 0 {
		rs = strings.Join(reqs, ", ")
	}
	fmt.Printf("Pod %s/%s  (requests %s)\n", r.Namespace, r.Pod, rs)
	fmt.Println(strings.Repeat("─", 64))
	for _, c := range r.Causes {
		fmt.Printf("  %s %s\n", c.Severity.Icon(), c.Title)
		for _, line := range strings.Split(c.Detail, "\n") {
			fmt.Printf("      %s\n", line)
		}
		fmt.Printf("      fix: %s\n\n", c.Fix)
	}

	if len(r.Candidates) > 0 {
		fmt.Printf("  ✓ would schedule on %d node(s):\n", len(r.Candidates))
		for _, c := range r.Candidates {
			fmt.Printf("      %s (free: %s)\n", c.Node, formatFree(c.Free))
		}
		fmt.Println()
	}
}

// formatFree renders a node's free GPU counts deterministically.
func formatFree(free map[string]int64) string {
	parts := make([]string, 0, len(free))
	for k, v := range free {
		parts = append(parts, fmt.Sprintf("%d %s", v, k))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}
