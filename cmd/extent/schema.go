package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// Every --json payload carries a top-level "schema" member naming its contract.
// The contract itself is described by schemas/<command>.v1.json at the
// repository root (for example schemas/stack-status.v1.json).
const (
	schemaVersion     = "extent.version/v1"
	schemaScan        = "extent.scan/v1"
	schemaPlan        = "extent.plan/v1"
	schemaAnalyze     = "extent.analyze/v1"
	schemaInstrument  = "extent.instrument/v1"
	schemaSmoke       = "extent.smoke/v1"
	schemaReport      = "extent.report/v1"
	schemaBaseline    = "extent.baseline/v1"
	schemaCardinality = "extent.cardinality/v1"
	schemaScore       = "extent.score/v1"
	schemaStackStatus = "extent.stack-status/v1"
)

// schemaFor returns the schema identifier for a command that does not have a
// dedicated constant above (verify and doctor share a payload shape but are
// distinct contracts).
func schemaFor(command string) string {
	return "extent." + command + "/v1"
}

// writeJSONSchema writes v as indented JSON to stdout with "schema" as the first
// top-level member. The payload is marshaled from its struct, so field order and
// value encoding are unchanged; only the schema member is prepended.
func writeJSONSchema(schema string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(raw) < 2 || raw[0] != '{' {
		return fmt.Errorf("%s payload is not a JSON object", schema)
	}
	name, err := json.Marshal(schema)
	if err != nil {
		return err
	}
	var joined bytes.Buffer
	joined.WriteString(`{"schema":`)
	joined.Write(name)
	if string(raw[1:]) != "}" {
		joined.WriteByte(',')
	}
	joined.Write(raw[1:])
	var out bytes.Buffer
	if err := json.Indent(&out, joined.Bytes(), "", "  "); err != nil {
		return err
	}
	out.WriteByte('\n')
	_, err = os.Stdout.Write(out.Bytes())
	return err
}
