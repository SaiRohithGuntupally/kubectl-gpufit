# kubectl-gpufit

> Stop grepping `kubectl describe nodes` for GPUs. Ask the cluster *why* your GPU pod won't schedule — and what fits where.

A `kubectl` plugin for **GPU scheduling & allocation diagnostics**, computed from
the Kubernetes API alone — **no DaemonSet, no agent, no metrics, no GPU hardware
to run it.** It deliberately stays out of the *utilization* lane (use
[`gpu-top`](https://github.com/jia-gao/kube-gpu-top) or
[`gpugo`](https://github.com/Tal-Naeh/kubectl-gpugo) for that) and answers the
questions the scheduler cares about.

```
$ kubectl gpufit
NODE         GPU RESOURCE             ALLOCATABLE  ALLOCATED  FREE  STATUS
gpu-a100-1   nvidia.com/gpu           8            6          2     Ready
gpu-a100-2   nvidia.com/gpu           8            8          0     Ready
gpu-mig-1    nvidia.com/mig-1g.10gb   56           56         0     tainted
```

```
$ kubectl gpufit why train-0 -n ml

Pod ml/train-0  (requests 2 nvidia.com/gpu)
────────────────────────────────────────────────────────────────
  ✗ GPU fragmentation — nvidia.com/gpu exists, but not on one node
      Pod needs 2 of "nvidia.com/gpu" on a single node. No node has that many
      free, though 3 are free across 2 node(s) in aggregate (largest single
      node free: 1).
      fix: Consolidate GPU workloads to free a contiguous set on one node, lower
           the pod's GPU count, or add a node with enough GPUs. Kubernetes
           cannot split one GPU request across nodes.
```

## What it detects (`gpufit why <pod>`)

**Extended-resource GPUs (`nvidia.com/gpu`, MIG, AMD/Intel):**
- **No node advertises the resource** — and it names the **exact broken link in
  the GPU enablement chain** (NFD → driver → container-toolkit → device-plugin →
  GFD → DCGM → MIG-manager) with the pod to debug, instead of a generic checklist.
- **GPU fragmentation** — enough GPUs in aggregate, but not on any single node.
- **Insufficient free GPUs** — including the whole-GPU-per-pod trap (an 8 GB job
  on an 80 GB A100 still consumes a full GPU without MIG/time-slicing).
- **Untolerated GPU taints** — free GPUs exist, but only on tainted nodes.

**Dynamic Resource Allocation (DRA, k8s 1.34+):** for pods using
`resourceClaims`, it resolves each claim (including template-generated ones) and
explains why it's unsatisfied — claim not created, **DeviceClass missing**, **no
DRA driver publishing ResourceSlices**, or, via **CEL device-selector matching
using the scheduler's own evaluator**, distinguishes *"no published device
satisfies your selectors"* from *"matching devices exist but are all in use."*

Each finding comes with a concrete fix. Exit code is `1` when a pod has a
blocking cause (scriptable), `0` otherwise.

## Install

```
kubectl krew install gpufit
```

## Roadmap

- `gpufit fit <manifest>` — pre-apply "will this GPU job schedule, and where?"
- Per-device "why excluded" — surface which specific selector/attribute rejected
  each device (today it reports match counts, not per-device reasons).

## Caveats

Best-effort static analysis from the Kubernetes API. It models GPU/accelerator
extended resources, the GPU enablement chain, and DRA claims (including CEL
device-selector matching via the scheduler's own evaluator) — but not GPU
utilization or priority/preemption. It reads cluster state (nodes, pods, DRA
objects) and makes no changes.

## License

MIT
