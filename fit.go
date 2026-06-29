package main

import (
	"context"
	"fmt"
	"os"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

// loadPodSpec decodes a manifest (YAML or JSON) into a Pod to simulate. It
// accepts a bare Pod or any common workload that carries a pod template
// (Deployment, StatefulSet, DaemonSet, ReplicaSet, Job, CronJob) and extracts
// that template.
func loadPodSpec(data []byte) (*corev1.Pod, error) {
	obj, _, err := scheme.Codecs.UniversalDeserializer().Decode(data, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("could not parse manifest: %w", err)
	}
	switch o := obj.(type) {
	case *corev1.Pod:
		return o, nil
	case *appsv1.Deployment:
		return podFromTemplate(o.Spec.Template, o.Namespace), nil
	case *appsv1.StatefulSet:
		return podFromTemplate(o.Spec.Template, o.Namespace), nil
	case *appsv1.DaemonSet:
		return podFromTemplate(o.Spec.Template, o.Namespace), nil
	case *appsv1.ReplicaSet:
		return podFromTemplate(o.Spec.Template, o.Namespace), nil
	case *batchv1.Job:
		return podFromTemplate(o.Spec.Template, o.Namespace), nil
	case *batchv1.CronJob:
		return podFromTemplate(o.Spec.JobTemplate.Spec.Template, o.Namespace), nil
	default:
		return nil, fmt.Errorf("unsupported kind %T (want a Pod or a workload with a pod template)", obj)
	}
}

func podFromTemplate(t corev1.PodTemplateSpec, ns string) *corev1.Pod {
	p := &corev1.Pod{ObjectMeta: t.ObjectMeta, Spec: t.Spec}
	if p.Namespace == "" {
		p.Namespace = ns
	}
	return p
}

// runFit simulates whether the pod described by a manifest would schedule onto
// the current cluster's GPUs, and reports whether it found a blocking cause.
func runFit(ctx context.Context, client kubernetes.Interface, opts options) (bool, error) {
	data, err := os.ReadFile(opts.file)
	if err != nil {
		return false, err
	}
	p, err := loadPodSpec(data)
	if err != nil {
		return false, err
	}
	if p.Namespace == "" {
		p.Namespace = opts.namespace
	}
	if p.Name == "" {
		p.Name = "(manifest)"
	}

	res, err := diagnosePodObj(ctx, client, p)
	if err != nil {
		return false, err
	}
	if structured(opts.output) {
		if err := emit(opts.output, res); err != nil {
			return false, err
		}
		return res.HasBlocker(), nil
	}
	printWhy(res)
	return res.HasBlocker(), nil
}
