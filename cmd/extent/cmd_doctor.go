package main

// runDoctor is the doctor command: the same checks as verify, reported under
// the doctor name in output and JSON schema selection.
func runDoctor(args []string) error {
	return runVerifyNamed("doctor", args)
}
