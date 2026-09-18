package smoke

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/Anubisx404/Extent/internal/observability"
)

type Config struct {
	URL               string
	PrometheusURL     string
	TempoURL          string
	LokiURL           string
	ServiceName       string
	Requests          int
	ExpectedMin       int
	ExpectedMax       int
	CorrelationHeader string
	SettleTimeout     time.Duration
	Duration          time.Duration
	Concurrency       int
	RateLimit         float64
}

type Report struct {
	OK                 bool          `json:"ok"`
	TargetVerified     bool          `json:"targetVerified"`
	CorrelationID      string        `json:"correlationId,omitempty"`
	Checks             []Check       `json:"checks"`
	TotalRequests      int           `json:"totalRequests,omitempty"`
	DurationElapsed    time.Duration `json:"durationElapsed,omitempty"`
	RPS                float64       `json:"rps,omitempty"`
	StatusDistribution map[int]int   `json:"statusDistribution,omitempty"`
	Concurrency        int           `json:"concurrency,omitempty"`
}

type Check struct {
	Name   string  `json:"name"`
	OK     bool    `json:"ok"`
	Value  float64 `json:"value,omitempty"`
	Status string  `json:"status,omitempty"`
	Scope  string  `json:"scope,omitempty"`
	Detail string  `json:"detail,omitempty"`
}

func Run(config Config) Report {
	if config.PrometheusURL == "" {
		config.PrometheusURL = "http://localhost:9090"
	}
	if config.Requests <= 0 {
		config.Requests = 3
	}
	if config.Concurrency <= 0 {
		config.Concurrency = 1
	}
	if config.ExpectedMin == 0 {
		config.ExpectedMin = 200
	}
	if config.ExpectedMax == 0 {
		config.ExpectedMax = 399
	}
	if config.CorrelationHeader == "" {
		config.CorrelationHeader = "X-Request-ID"
	}
	if config.SettleTimeout <= 0 {
		config.SettleTimeout = 15 * time.Second
	}
	if config.ExpectedMin < 100 || config.ExpectedMax > 599 || config.ExpectedMin > config.ExpectedMax {
		return Report{OK: false, Checks: []Check{{Name: "configuration", Status: "failed", Detail: "invalid expected status range"}}}
	}
	target, err := url.Parse(config.URL)
	if err != nil || target.Host == "" || target.User != nil || target.Scheme != "http" && target.Scheme != "https" {
		return Report{OK: false, Checks: []Check{{Name: "configuration", Status: "failed", Detail: "application URL must be HTTP(S) without userinfo"}}}
	}
	var tokenBytes [16]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return Report{OK: false, Checks: []Check{{Name: "correlation token", OK: false, Detail: err.Error()}}}
	}
	token := hex.EncodeToString(tokenBytes[:])
	client := &http.Client{Timeout: 5 * time.Second}
	queries := []struct {
		name string
		expr string
	}{
		{name: "traces accepted", expr: "sum(otelcol_receiver_accepted_spans)"},
		{name: "logs accepted", expr: "sum(otelcol_receiver_accepted_log_records)"},
		{name: "metrics accepted", expr: "sum(otelcol_receiver_accepted_metric_points)"},
	}
	before := make(map[string]float64, len(queries))
	beforeErr := make(map[string]error, len(queries))
	for _, query := range queries {
		before[query.name], beforeErr[query.name] = queryPrometheus(client, config.PrometheusURL, query.expr)
	}
	targetMetricExpression := ""
	targetMetricBefore := 0.0
	var targetMetricBeforeErr error
	if config.ServiceName != "" {
		targetMetricExpression = fmt.Sprintf(`sum(http_server_duration_milliseconds_count{service_name=%s})`, strconv.Quote(config.ServiceName))
		targetMetricBefore, targetMetricBeforeErr = queryPrometheus(client, config.PrometheusURL, targetMetricExpression)
	}
	checks := []Check{}

	var wg sync.WaitGroup
	var mu sync.Mutex
	statusDist := make(map[int]int)
	totalRequests := 0
	var firstReqErr error
	unexpectedStatusCount := 0

	startTime := time.Now()

	var ticker *time.Ticker
	if config.RateLimit > 0 {
		ticker = time.NewTicker(time.Duration(float64(time.Second) / config.RateLimit))
		defer ticker.Stop()
	}

	sendOne := func() bool {
		req, err := http.NewRequest(http.MethodGet, target.String(), nil)
		if err != nil {
			mu.Lock()
			if firstReqErr == nil {
				firstReqErr = err
			}
			mu.Unlock()
			return false
		}
		req.Header.Set(config.CorrelationHeader, token)
		req.Header.Set("traceparent", "00-"+token+"-"+token[:16]+"-01")
		resp, err := client.Do(req)
		if err != nil {
			mu.Lock()
			if firstReqErr == nil {
				firstReqErr = err
			}
			mu.Unlock()
			return false
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()

		mu.Lock()
		totalRequests++
		statusDist[resp.StatusCode]++
		if resp.StatusCode < config.ExpectedMin || resp.StatusCode > config.ExpectedMax {
			unexpectedStatusCount++
		}
		mu.Unlock()
		return true
	}

	if config.Duration > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), config.Duration)
		defer cancel()

		for w := 0; w < config.Concurrency; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-ctx.Done():
						return
					default:
					}
					if ticker != nil {
						select {
						case <-ctx.Done():
							return
						case <-ticker.C:
						}
					}
					if !sendOne() && firstReqErr != nil {
						return
					}
				}
			}()
		}
		wg.Wait()
	} else {
		jobs := make(chan struct{}, config.Requests)
		for i := 0; i < config.Requests; i++ {
			jobs <- struct{}{}
		}
		close(jobs)

		for w := 0; w < config.Concurrency; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range jobs {
					if ticker != nil {
						<-ticker.C
					}
					if !sendOne() && firstReqErr != nil {
						return
					}
				}
			}()
		}
		wg.Wait()
	}

	durationElapsed := time.Since(startTime)
	rps := 0.0
	if durationElapsed.Seconds() > 0 {
		rps = float64(totalRequests) / durationElapsed.Seconds()
	}

	if totalRequests == 0 && firstReqErr != nil {
		return Report{
			OK:                 false,
			TargetVerified:     false,
			CorrelationID:      token,
			Checks:             []Check{{Name: "app request", OK: false, Detail: firstReqErr.Error()}},
			TotalRequests:      totalRequests,
			DurationElapsed:    durationElapsed,
			RPS:                rps,
			StatusDistribution: statusDist,
			Concurrency:        config.Concurrency,
		}
	}

	if unexpectedStatusCount > 0 {
		checks = append(checks, Check{
			Name:   "synthetic app requests",
			OK:     false,
			Status: "failed",
			Scope:  "target",
			Value:  float64(totalRequests),
			Detail: fmt.Sprintf("%d request(s) returned status outside expected range %d-%d", unexpectedStatusCount, config.ExpectedMin, config.ExpectedMax),
		})
	} else {
		checks = append(checks, Check{
			Name:   "synthetic app requests",
			OK:     true,
			Status: "passed",
			Scope:  "target",
			Value:  float64(totalRequests),
			Detail: fmt.Sprintf("%d request(s) returned %d-%d", totalRequests, config.ExpectedMin, config.ExpectedMax),
		})
	}
	if firstReqErr != nil {
		checks = append(checks, Check{Name: "app request", OK: false, Status: "failed", Scope: "target", Detail: firstReqErr.Error()})
	}

	after := make(map[string]float64, len(queries))
	afterErr := make(map[string]error, len(queries))
	deadline := time.Now().Add(config.SettleTimeout)
	for {
		allPositive := true
		for _, query := range queries {
			after[query.name], afterErr[query.name] = queryPrometheus(client, config.PrometheusURL, query.expr)
			if afterErr[query.name] != nil || after[query.name]-before[query.name] <= 0 {
				allPositive = false
			}
		}
		if allPositive || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	for _, query := range queries {
		check := Check{Name: query.name, Scope: "global", Status: "unavailable"}
		if beforeErr[query.name] != nil {
			check.Detail = "before query failed: " + beforeErr[query.name].Error()
		} else if afterErr[query.name] != nil {
			check.Detail = "after query failed: " + afterErr[query.name].Error()
		} else {
			check.Value = after[query.name] - before[query.name]
			if check.Value > 0 {
				check.OK = true
				check.Status = "supporting"
				check.Detail = "global Collector delta observed; this does not prove the telemetry belongs to the target service"
			} else {
				check.Status = "missing"
				check.Detail = "no positive post-request Collector delta"
			}
		}
		checks = append(checks, check)
	}
	if config.ServiceName != "" {
		check := Check{Name: "target service metrics", Scope: "target", Status: "missing"}
		metricDeadline := time.Now().Add(config.SettleTimeout)
		lastMetric := targetMetricBefore
		for {
			afterMetric, afterMetricErr := queryPrometheus(client, config.PrometheusURL, targetMetricExpression)
			if targetMetricBeforeErr != nil {
				check.Status = "unavailable"
				check.Detail = "before query failed: " + targetMetricBeforeErr.Error()
				break
			}
			if afterMetricErr != nil {
				check.Status = "unavailable"
				check.Detail = "after query failed: " + afterMetricErr.Error()
				break
			}
			check.Value = afterMetric - targetMetricBefore
			lastMetric = afterMetric
			if check.Value > 0 {
				check.OK = true
				check.Status = "passed"
				check.Detail = "service-labeled HTTP metric increased after synthetic requests"
				break
			}
			if !time.Now().Before(metricDeadline) {
				check.Detail = fmt.Sprintf("service-labeled HTTP metric did not increase (before %.0f, after %.0f)", targetMetricBefore, lastMetric)
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		checks = append(checks, check)
	}
	targetVerified := false
	if config.ServiceName == "" {
		checks = append(checks, Check{Name: "target service trace", Scope: "target", Status: "unknown", Detail: "service name is required for target-specific trace verification"})
	} else {
		if config.TempoURL == "" {
			config.TempoURL = "http://localhost:3200"
		}
		check := Check{Name: "target service trace", Scope: "target", Status: "missing"}
		traceDeadline := time.Now().Add(config.SettleTimeout)
		for {
			count, err := queryTempoCorrelation(client, config.TempoURL, config.ServiceName, token)
			if err != nil {
				check.Status = "unavailable"
				check.Detail = err.Error()
				break
			}
			if count > 0 {
				check.OK = true
				check.Status = "passed"
				check.Value = float64(count)
				check.Detail = "Tempo returned a service trace containing the synthetic request correlation ID"
				targetVerified = true
				break
			}
			if !time.Now().Before(traceDeadline) {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		if check.Detail == "" {
			check.Detail = "no correlated target-service trace found"
		}
		checks = append(checks, check)
	}
	if config.ServiceName != "" {
		if config.LokiURL == "" {
			config.LokiURL = "http://localhost:3100"
		}
		check := Check{Name: "target service log", Scope: "target", Status: "missing"}
		logDeadline := time.Now().Add(config.SettleTimeout)
		for {
			count, err := queryLokiCorrelation(client, config.LokiURL, config.ServiceName, token)
			if err != nil {
				check.Status = "unavailable"
				check.Detail = err.Error()
				break
			}
			if count > 0 {
				check.OK = true
				check.Status = "passed"
				check.Value = float64(count)
				check.Detail = "Loki returned a service log containing the synthetic request correlation ID"
				break
			}
			if !time.Now().Before(logDeadline) {
				check.Detail = "no correlated target-service log found"
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		checks = append(checks, check)
	}

	ok := true
	for _, check := range checks {
		if check.Scope == "target" && !check.OK {
			ok = false
			break
		}
	}
	return Report{
		OK:                 ok,
		TargetVerified:     targetVerified,
		CorrelationID:      token,
		Checks:             checks,
		TotalRequests:      totalRequests,
		DurationElapsed:    durationElapsed,
		RPS:                rps,
		StatusDistribution: statusDist,
		Concurrency:        config.Concurrency,
	}
}

func queryTempoCorrelation(client *http.Client, baseURL, service, token string) (int, error) {
	base, err := observability.ValidateURL(baseURL)
	if err != nil {
		return 0, err
	}
	bounded := &observability.Client{HTTP: client, BodyLimit: observability.DefaultBodyLimit}
	response, err := bounded.Do(context.Background(), http.MethodGet, base, "/api/traces/"+token, nil, nil)
	if err != nil {
		var requestError *observability.Error
		if errors.As(err, &requestError) && requestError.StatusCode == http.StatusNotFound {
			return 0, nil
		}
		return 0, err
	}
	var payload struct {
		Batches []struct {
			Resource struct {
				Attributes []tempoAttribute `json:"attributes"`
			} `json:"resource"`
			ScopeSpans []struct {
				Spans []struct {
					Attributes []tempoAttribute `json:"attributes"`
				} `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"batches"`
	}
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		return 0, err
	}
	for _, batch := range payload.Batches {
		if !hasTempoAttribute(batch.Resource.Attributes, "service.name", service) {
			continue
		}
		for _, scope := range batch.ScopeSpans {
			for _, span := range scope.Spans {
				if hasTempoAttribute(span.Attributes, "http.request.header.x_request_id", token) {
					return 1, nil
				}
			}
		}
	}
	return 0, nil
}

type tempoAttribute struct {
	Key   string     `json:"key"`
	Value tempoValue `json:"value"`
}

type tempoValue struct {
	StringValue string `json:"stringValue"`
	ArrayValue  struct {
		Values []tempoValue `json:"values"`
	} `json:"arrayValue"`
}

func hasTempoAttribute(attributes []tempoAttribute, key, want string) bool {
	for _, attribute := range attributes {
		if attribute.Key != key {
			continue
		}
		if attribute.Value.StringValue == want {
			return true
		}
		for _, value := range attribute.Value.ArrayValue.Values {
			if value.StringValue == want {
				return true
			}
		}
	}
	return false
}

func queryLokiCorrelation(client *http.Client, baseURL, service, token string) (int, error) {
	base, err := observability.ValidateURL(baseURL)
	if err != nil {
		return 0, err
	}
	bounded := &observability.Client{HTTP: client, BodyLimit: observability.DefaultBodyLimit}
	for _, correlationField := range []string{"http_request_header_x_request_id", "trace_id"} {
		expr := fmt.Sprintf(`{service_name=%s} | %s = %s`, strconv.Quote(service), correlationField, strconv.Quote(token))
		values := url.Values{"query": {expr}, "limit": {"20"}}
		response, err := bounded.Do(context.Background(), http.MethodGet, base, "/loki/api/v1/query_range", values, nil)
		if err != nil {
			return 0, err
		}
		var payload struct {
			Status string `json:"status"`
			Data   struct {
				Result []json.RawMessage `json:"result"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body, &payload); err != nil {
			return 0, err
		}
		if payload.Status != "success" {
			return 0, fmt.Errorf("Loki returned status %q", payload.Status)
		}
		if len(payload.Data.Result) > 0 {
			return len(payload.Data.Result), nil
		}
	}
	return 0, nil
}

func queryPrometheus(client *http.Client, baseURL, expr string) (float64, error) {
	endpoint, err := observability.ValidateURL(baseURL)
	if err != nil {
		return 0, err
	}
	values := url.Values{}
	values.Set("query", expr)
	bounded := &observability.Client{HTTP: client, BodyLimit: observability.DefaultBodyLimit}
	resp, err := bounded.Do(context.Background(), http.MethodGet, endpoint, "/api/v1/query", values, nil)
	if err != nil {
		return 0, err
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Value []any `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(bytes.NewReader(resp.Body)).Decode(&payload); err != nil {
		return 0, err
	}
	if payload.Status != "success" || len(payload.Data.Result) == 0 || len(payload.Data.Result[0].Value) < 2 {
		return 0, nil
	}
	raw, ok := payload.Data.Result[0].Value[1].(string)
	if !ok {
		return 0, nil
	}
	return strconv.ParseFloat(raw, 64)
}
