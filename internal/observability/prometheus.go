package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Sample struct {
	Metric map[string]string `json:"metric"`
	Value  float64           `json:"value"`
}
type Prometheus struct {
	Client *Client
	Base   *url.URL
}

func NewPrometheus(raw string, timeout time.Duration) (*Prometheus, error) {
	u, e := ValidateURL(raw)
	if e != nil {
		return nil, e
	}
	return &Prometheus{NewClient(timeout, DefaultBodyLimit), u}, nil
}
func (p *Prometheus) Query(ctx context.Context, expr, service, lookback string) ([]Sample, error) {
	if service == "" {
		return nil, fmt.Errorf("service name is required")
	}
	if lookback == "" {
		lookback = "5m"
	}
	duration, err := time.ParseDuration(lookback)
	if err != nil || duration <= 0 || duration > 30*24*time.Hour {
		return nil, fmt.Errorf("invalid lookback %q", lookback)
	}
	if !strings.Contains(expr, "$service") || !strings.Contains(expr, "$lookback") {
		return nil, fmt.Errorf("Prometheus expression must contain $service and $lookback placeholders")
	}
	expr = strings.ReplaceAll(expr, "$service", strconv.Quote(service))
	expr = strings.ReplaceAll(expr, "$lookback", lookback)
	q := url.Values{"query": {expr}}
	r, e := p.Client.Do(ctx, "GET", p.Base, "/api/v1/query", q, nil)
	if e != nil {
		return nil, e
	}
	var v struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  []any             `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if e = json.Unmarshal(r.Body, &v); e != nil {
		return nil, e
	}
	if v.Status != "success" {
		return nil, fmt.Errorf("prometheus query status %q", v.Status)
	}
	out := make([]Sample, 0, len(v.Data.Result))
	for _, x := range v.Data.Result {
		if len(x.Value) < 2 {
			continue
		}
		s, _ := x.Value[1].(string)
		n, err := strconv.ParseFloat(s, 64)
		if err != nil {
			continue
		}
		out = append(out, Sample{x.Metric, n})
	}
	return out, nil
}
