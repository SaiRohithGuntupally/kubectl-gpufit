package gpu

import (
	"context"

	resourcev1 "k8s.io/api/resource/v1"
	dracel "k8s.io/dynamic-resource-allocation/cel"
)

// matchDevices evaluates the given CEL selector expressions (drawn from a
// DeviceClass and/or a device request) against every device published in the
// ResourceSlices, using the scheduler's own CEL evaluator so the verdict matches
// real allocation behavior. It returns how many devices were evaluated, how many
// satisfy ALL expressions, and a compile-error string if any expression failed
// to compile.
func matchDevices(exprs []string, slices []resourcev1.ResourceSlice) (evaluated, matched int, compileErr string) {
	compiler := dracel.GetCompiler(dracel.Features{})
	programs := make([]dracel.CompilationResult, 0, len(exprs))
	for _, e := range exprs {
		r := compiler.CompileCELExpression(e, dracel.Options{})
		if r.Error != nil {
			return 0, 0, r.Error.Error()
		}
		programs = append(programs, r)
	}

	ctx := context.Background()
	for i := range slices {
		for j := range slices[i].Spec.Devices {
			d := &slices[i].Spec.Devices[j]
			evaluated++
			dev := dracel.Device{
				Driver:     slices[i].Spec.Driver,
				Attributes: d.Attributes,
				Capacity:   d.Capacity,
			}
			all := true
			for k := range programs {
				ok, _, err := programs[k].DeviceMatches(ctx, dev)
				if err != nil || !ok {
					all = false
					break
				}
			}
			if all {
				matched++
			}
		}
	}
	return evaluated, matched, ""
}

// requestSelectors returns the CEL selector expressions in effect for a device
// request: the referenced DeviceClass's selectors plus the request's own.
func requestSelectors(req *resourcev1.ExactDeviceRequest, classes map[string]*resourcev1.DeviceClass) []string {
	var exprs []string
	if dc := classes[req.DeviceClassName]; dc != nil {
		for _, s := range dc.Spec.Selectors {
			if s.CEL != nil && s.CEL.Expression != "" {
				exprs = append(exprs, s.CEL.Expression)
			}
		}
	}
	for _, s := range req.Selectors {
		if s.CEL != nil && s.CEL.Expression != "" {
			exprs = append(exprs, s.CEL.Expression)
		}
	}
	return exprs
}
