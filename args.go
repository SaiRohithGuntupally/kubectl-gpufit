package main

import (
	"fmt"
	"os"
)

type command int

const (
	cmdAlloc command = iota
	cmdWhy
	cmdFit
)

type options struct {
	command    command
	namespace  string
	context    string
	kubeconfig string
	podName    string
	file       string // manifest path for `fit`
	output     string // "", "text", "json", "yaml"
	noColor    bool
}

// parseArgs is a small hand-rolled parser so flags may appear before or after
// the subcommand/pod name (kubectl plugins receive args in any order).
func parseArgs(args []string) (options, error) {
	var o options
	var positionals []string
	needValue := func(i int, name string) (string, int, error) {
		if i+1 >= len(args) {
			return "", i, fmt.Errorf("flag %s needs a value", name)
		}
		return args[i+1], i + 1, nil
	}

	for i := 0; i < len(args); i++ {
		a := args[i]
		var err error
		switch {
		case a == "-h" || a == "--help":
			fmt.Print(usage)
			os.Exit(0)
		case a == "--no-color":
			o.noColor = true
		case a == "-n" || a == "--namespace":
			o.namespace, i, err = needValue(i, a)
		case a == "-o" || a == "--output":
			o.output, i, err = needValue(i, a)
		case a == "--context":
			o.context, i, err = needValue(i, a)
		case a == "--kubeconfig":
			o.kubeconfig, i, err = needValue(i, a)
		case len(a) > 1 && a[0] == '-':
			return o, fmt.Errorf("unknown flag %q", a)
		default:
			positionals = append(positionals, a)
		}
		if err != nil {
			return o, err
		}
	}

	switch {
	case len(positionals) == 0:
		o.command = cmdAlloc
	case positionals[0] == "why":
		o.command = cmdWhy
		rest := positionals[1:]
		if len(rest) == 0 {
			return o, fmt.Errorf("`gpufit why` needs a pod name")
		}
		if len(rest) > 1 {
			return o, fmt.Errorf("unexpected extra argument %q", rest[1])
		}
		o.podName = rest[0]
	case positionals[0] == "fit":
		o.command = cmdFit
		rest := positionals[1:]
		if len(rest) == 0 {
			return o, fmt.Errorf("`gpufit fit` needs a manifest file path")
		}
		if len(rest) > 1 {
			return o, fmt.Errorf("unexpected extra argument %q", rest[1])
		}
		o.file = rest[0]
	default:
		return o, fmt.Errorf("unknown command %q (did you mean `gpufit why %s`?)", positionals[0], positionals[0])
	}
	return o, nil
}
