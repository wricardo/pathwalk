package pathwalk

import "fmt"

// QualityGateType identifies what a quality gate checks.
type QualityGateType string

const (
	// QualityGateTerminationReason passes when the run ended with the expected Reason.
	// If Expected is empty, "terminal" is assumed.
	QualityGateTerminationReason QualityGateType = "termination_reason"

	// QualityGateRequiredVariables passes when all named variables were extracted
	// and are non-empty in the run result.
	QualityGateRequiredVariables QualityGateType = "required_variables"

	// QualityGateOutputNotEmpty passes when the run produced non-empty output.
	QualityGateOutputNotEmpty QualityGateType = "output_not_empty"
)

// QualityGateSpec declares a single outcome check for a pathway run.
type QualityGateSpec struct {
	// Name is a human-readable label for the gate (used in results and reporting).
	Name string `json:"name"`
	// Type determines what is checked.
	Type QualityGateType `json:"type"`
	// Expected is the required Reason value for QualityGateTerminationReason gates.
	// Defaults to "terminal" when empty.
	Expected string `json:"expected,omitempty"`
	// Variables lists variable names that must be non-empty for
	// QualityGateRequiredVariables gates.
	Variables []string `json:"variables,omitempty"`
}

// QualityGateResult records the outcome of a single gate evaluation.
type QualityGateResult struct {
	Name   string  `json:"name"`
	Passed bool    `json:"passed"`
	Reason *string `json:"reason,omitempty"` // non-nil when Passed is false
}

// EvaluateGates checks each gate spec against a completed run result and returns
// one result per spec. Order is preserved.
func EvaluateGates(specs []QualityGateSpec, result *RunResult) []QualityGateResult {
	out := make([]QualityGateResult, 0, len(specs))
	for _, spec := range specs {
		gr := QualityGateResult{Name: spec.Name}
		switch spec.Type {
		case QualityGateTerminationReason:
			expected := spec.Expected
			if expected == "" {
				expected = "terminal"
			}
			if result.Reason == expected {
				gr.Passed = true
			} else {
				reason := fmt.Sprintf("reason was %q, expected %q", result.Reason, expected)
				gr.Reason = &reason
			}

		case QualityGateRequiredVariables:
			var missing []string
			for _, varName := range spec.Variables {
				v, ok := result.Variables[varName]
				if !ok || v == nil || fmt.Sprintf("%v", v) == "" {
					missing = append(missing, varName)
				}
			}
			if len(missing) == 0 {
				gr.Passed = true
			} else {
				reason := fmt.Sprintf("missing variables: %v", missing)
				gr.Reason = &reason
			}

		case QualityGateOutputNotEmpty:
			if result.Output != "" {
				gr.Passed = true
			} else {
				reason := "output was empty"
				gr.Reason = &reason
			}
		}
		out = append(out, gr)
	}
	return out
}

// KPIType identifies how a KPI value is computed.
type KPIType string

const (
	// KPITypeCount contributes 1.0 to a count metric when the gate passes, 0 otherwise.
	KPITypeCount KPIType = "count"
	// KPITypeRate is like Count; callers aggregate across runs to compute a rate.
	KPITypeRate KPIType = "rate"
	// KPITypeSum extracts a numeric variable and adds it to a running total.
	KPITypeSum KPIType = "sum"
	// KPITypeAverage extracts a numeric variable; callers average values across runs.
	KPITypeAverage KPIType = "average"
)

// KPISpec declares a per-run metric contribution.
type KPISpec struct {
	Name string  `json:"name"`
	Type KPIType `json:"type"`
	// Gate is the QualityGateSpec.Name to check for Count/Rate KPIs.
	Gate string `json:"gate,omitempty"`
	// Var is the variable name to read for Sum/Average KPIs.
	Var  string `json:"var,omitempty"`
	Unit string `json:"unit,omitempty"`
}

// KPIResult is the per-run contribution value for a KPI.
type KPIResult struct {
	Name  string   `json:"name"`
	Type  KPIType  `json:"type"`
	Unit  string   `json:"unit,omitempty"`
	Value *float64 `json:"value,omitempty"` // nil when the variable was missing or non-numeric
}

// EvaluateKPIs computes the per-run KPI contribution from a completed run result
// and the gate results already evaluated for the same run.
func EvaluateKPIs(specs []KPISpec, result *RunResult, gateResults []QualityGateResult) []KPIResult {
	gatePassed := make(map[string]bool, len(gateResults))
	for _, g := range gateResults {
		gatePassed[g.Name] = g.Passed
	}

	out := make([]KPIResult, 0, len(specs))
	for _, spec := range specs {
		kr := KPIResult{Name: spec.Name, Type: spec.Type, Unit: spec.Unit}
		switch spec.Type {
		case KPITypeCount, KPITypeRate:
			v := 0.0
			if gatePassed[spec.Gate] {
				v = 1.0
			}
			kr.Value = &v

		case KPITypeSum, KPITypeAverage:
			raw, ok := result.Variables[spec.Var]
			if ok && raw != nil {
				var f float64
				switch tv := raw.(type) {
				case float64:
					f = tv
					kr.Value = &f
				case string:
					if _, err := fmt.Sscanf(tv, "%f", &f); err == nil {
						kr.Value = &f
					}
				}
			}
		}
		out = append(out, kr)
	}
	return out
}
