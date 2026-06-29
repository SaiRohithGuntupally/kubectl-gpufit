// kubectl-gpufit — GPU scheduling & allocation diagnostics, computed from the
// Kubernetes API alone (no DaemonSet, agent, or metrics).
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/SaiRohithGuntupally/kubectl-gpufit/pkg/gpu"
)

const usage = `kubectl gpufit — GPU scheduling & allocation diagnostics

USAGE:
  kubectl gpufit [flags]              show GPU allocatable vs allocated per node
  kubectl gpufit why <pod> [flags]    explain why a GPU pod can't be scheduled

FLAGS:
  -n, --namespace <ns>     namespace for 'why' (default: current context namespace)
  -o, --output <format>    output format: text (default), json, or yaml
      --context <name>     kubeconfig context to use
      --kubeconfig <path>  path to kubeconfig
      --no-color           disable colored output
  -h, --help               show this help

EXIT CODES:
  0  no GPU scheduling blocker (or allocation view printed)
  1  the pod has a GPU scheduling blocker
  2  usage error
  3  runtime error (e.g. could not reach the cluster)

GPU data comes from the Kubernetes API only — no DaemonSet, agent, or metrics
required. For per-pod GPU *utilization*, see the gpu-top or gpugo plugins.
`

func main() {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		fmt.Fprint(os.Stderr, "\n"+usage)
		os.Exit(2)
	}
	switch opts.output {
	case "", "text", "json", "yaml":
	default:
		fmt.Fprintf(os.Stderr, "error: invalid --output %q (want text, json, or yaml)\n", opts.output)
		os.Exit(2)
	}

	client, defaultNS, err := newClient(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: could not connect to cluster:", err)
		os.Exit(3)
	}
	if opts.namespace == "" {
		opts.namespace = defaultNS
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	switch opts.command {
	case cmdWhy:
		blocked, err := runWhy(ctx, client, opts)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(3)
		}
		if blocked {
			os.Exit(1)
		}
	default:
		if err := runAlloc(ctx, client, opts); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(3)
		}
	}
}

// runAlloc prints the cluster-wide GPU allocation view.
func runAlloc(ctx context.Context, client kubernetes.Interface, opts options) error {
	view, _, err := gatherGPU(ctx, client)
	if err != nil {
		return err
	}
	if structured(opts.output) {
		return emit(opts.output, view)
	}
	printAlloc(view)
	return nil
}

// runWhy diagnoses why a single pod's GPU request can't be scheduled and reports
// whether it found a blocking cause.
func runWhy(ctx context.Context, client kubernetes.Interface, opts options) (bool, error) {
	p, err := client.CoreV1().Pods(opts.namespace).Get(ctx, opts.podName, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	view, chain, err := gatherGPU(ctx, client)
	if err != nil {
		return false, err
	}
	res := gpu.Diagnose(p, view, &chain)
	if structured(opts.output) {
		if err := emit(opts.output, res); err != nil {
			return false, err
		}
		return res.HasBlocker(), nil
	}
	printWhy(res)
	return res.HasBlocker(), nil
}

// gatherGPU lists nodes and pods once and derives both the GPU allocation view
// and the GPU enablement-chain status from the same snapshot.
func gatherGPU(ctx context.Context, client kubernetes.Interface) ([]gpu.NodeGPU, gpu.ChainStatus, error) {
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, gpu.ChainStatus{}, err
	}
	pods, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, gpu.ChainStatus{}, err
	}
	return gpu.Cluster(nodes.Items, pods.Items), gpu.AnalyzeOperatorChain(pods.Items), nil
}
