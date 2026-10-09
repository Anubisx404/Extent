package instrumenter

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
)

// versions.json holds the OpenTelemetry dependency pins the instrumenter writes
// into target projects. It is embedded and decoded once at package
// initialization; scripts/bump-otel.sh shows the latest upstream releases and
// can update it.
//
//go:embed versions.json
var versionsData []byte

type versionPins struct {
	Comment string `json:"_comment,omitempty"`
	Node    struct {
		Dependencies map[string]string `json:"dependencies"`
		Database     map[string]string `json:"database"`
		ESM          map[string]string `json:"esm"`
	} `json:"node"`
	Python struct {
		Dependencies map[string]string `json:"dependencies"`
	} `json:"python"`
	Go struct {
		Modules []goModulePin `json:"modules"`
	} `json:"go"`
}

type goModulePin struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

// pins is the decoded versions.json. The per-language pin variables below are
// views of it, so the pin data lives in one place.
var pins = loadVersionPins(versionsData)

func loadVersionPins(data []byte) versionPins {
	var decoded versionPins
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		panic(fmt.Sprintf("instrumenter/versions.json is invalid: %v", err))
	}
	return decoded
}
