package main

import (
	"testing"

	"github.com/SaiRohithGuntupally/kubectl-gpufit/pkg/gpu"
)

func TestLoadPodSpec_Pod(t *testing.T) {
	y := []byte(`
apiVersion: v1
kind: Pod
metadata:
  name: train
  namespace: ml
spec:
  containers:
  - name: c
    resources:
      limits:
        nvidia.com/gpu: "2"
`)
	p, err := loadPodSpec(y)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "train" || p.Namespace != "ml" {
		t.Errorf("metadata not preserved: %s/%s", p.Namespace, p.Name)
	}
	if gpu.PodGPURequests(p)["nvidia.com/gpu"] != 2 {
		t.Errorf("expected 2 gpu requests, got %v", gpu.PodGPURequests(p))
	}
}

func TestLoadPodSpec_DeploymentTemplate(t *testing.T) {
	y := []byte(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: trainer
  namespace: ml
spec:
  template:
    spec:
      containers:
      - name: c
        resources:
          limits:
            nvidia.com/gpu: "4"
`)
	p, err := loadPodSpec(y)
	if err != nil {
		t.Fatal(err)
	}
	if p.Namespace != "ml" {
		t.Errorf("namespace should be inherited from workload, got %q", p.Namespace)
	}
	if gpu.PodGPURequests(p)["nvidia.com/gpu"] != 4 {
		t.Errorf("expected 4 gpu requests from the pod template, got %v", gpu.PodGPURequests(p))
	}
}

func TestLoadPodSpec_UnsupportedKind(t *testing.T) {
	y := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n")
	if _, err := loadPodSpec(y); err == nil {
		t.Fatal("expected an error for an unsupported kind")
	}
}
