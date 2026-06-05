//go:build wasip1 || wasm

package main

import "github.com/extism/go-pdk"

func main() {}

//go:wasmexport execute_step
func executeStep() int32 {
	var in executeStepInput
	if err := pdk.InputJSON(&in); err != nil {
		pdk.SetError(err)
		return 1
	}
	return writeOutput(runSQLiteStep(in))
}

func writeOutput(out executeStepOutput) int32 {
	if err := pdk.OutputJSON(out); err != nil {
		pdk.SetError(err)
		return 1
	}
	return 0
}
