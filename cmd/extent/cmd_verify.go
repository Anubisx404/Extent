package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/Anubisx404/Extent/internal/verifier"
)

type verifyOptions struct {
	JSON            bool
	URL             string
	Prometheus      string
	Loki            string
	Tempo           string
	Grafana         string
	GrafanaUser     string
	GrafanaPassword string
	GrafanaToken    string
	Service         string
	Requests        int
}

func newVerifyFlagSet(command string, o *verifyOptions) *flag.FlagSet {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.BoolVar(&o.JSON, "json", false, "print JSON output")
	fs.StringVar(&o.URL, "url", "", "application URL to request for end-to-end telemetry verification")
	fs.StringVar(&o.Prometheus, "prometheus", "http://localhost:9090", "Prometheus base URL")
	fs.StringVar(&o.Loki, "loki", "http://localhost:3100", "Loki base URL")
	fs.StringVar(&o.Tempo, "tempo", "http://localhost:3200", "Tempo base URL")
	fs.StringVar(&o.Grafana, "grafana", "", "Grafana base URL for live datasource correlation validation")
	fs.StringVar(&o.GrafanaUser, "grafana-user", "", "Grafana username for basic-auth API checks")
	fs.StringVar(&o.GrafanaPassword, "grafana-password", "", "Grafana password for basic-auth API checks")
	fs.StringVar(&o.GrafanaToken, "grafana-token", "", "Grafana service account token for Bearer API checks; takes precedence over basic auth")
	fs.StringVar(&o.Service, "service", "", "target service.name for correlated telemetry verification")
	fs.IntVar(&o.Requests, "requests", 3, "number of synthetic app requests when --url is set")
	return fs
}

func runVerify(args []string) error {
	return runVerifyNamed("verify", args)
}

func runVerifyNamed(command string, args []string) error {
	var o verifyOptions
	fs := newVerifyFlagSet(command, &o)
	if help, err := parseFlags(fs, args); help || err != nil {
		return err
	}
	if err := validatePositional(command, fs.Args(), 1); err != nil {
		return err
	}
	if err := validateRequests(o.Requests); err != nil {
		return err
	}
	root := firstArgOrDot(fs.Args())
	if o.URL != "" && strings.TrimSpace(o.Service) == "" {
		return usageError(command + " requires --service when --url is set")
	}
	report := verifier.VerifyConfig(verifier.Config{
		Root:            root,
		URL:             o.URL,
		PrometheusURL:   o.Prometheus,
		LokiURL:         o.Loki,
		TempoURL:        o.Tempo,
		GrafanaURL:      o.Grafana,
		GrafanaUser:     o.GrafanaUser,
		GrafanaPassword: o.GrafanaPassword,
		GrafanaToken:    o.GrafanaToken,
		ServiceName:     o.Service,
		Requests:        o.Requests,
	})
	if o.JSON {
		if err := writeJSONSchema(schemaFor(command), report); err != nil {
			return err
		}
		if !report.OK {
			return verificationError("verification failed")
		}
		return nil
	}
	for _, check := range report.Checks {
		status := "FAIL"
		if check.OK {
			status = "OK"
		}
		fmt.Printf("[%s] %s", status, check.Name)
		if check.Detail != "" {
			fmt.Printf(" - %s", check.Detail)
		}
		fmt.Println()
	}
	if !report.OK {
		return verificationError("verification failed")
	}
	return nil
}
