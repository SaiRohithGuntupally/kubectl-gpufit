package main

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// newClient builds a Kubernetes client from the standard kubeconfig resolution
// rules, honoring --kubeconfig and --context overrides. It also returns the
// namespace from the resolved context (defaulting to "default").
func newClient(opts options) (kubernetes.Interface, string, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if opts.kubeconfig != "" {
		rules.ExplicitPath = opts.kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if opts.context != "" {
		overrides.CurrentContext = opts.context
	}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)

	ns, _, _ := cc.Namespace()
	if ns == "" {
		ns = "default"
	}
	cfg, err := cc.ClientConfig()
	if err != nil {
		return nil, "", err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	return cs, ns, err
}
