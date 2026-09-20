package api

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/domain"
)

// The five fields were the whole vocabulary, so anything else a host could
// say about itself had nowhere to go: an OOM kill, a throttled container, the
// usage of a mount that is not "/". A rule can be written against any of them
// now, by name, with no change here.
func TestARuleCanBeWrittenAgainstANamedReading(t *testing.T) {
	sample := domain.MetricSample{
		CPU: 12, Memory: 40,
		Values: map[string]float64{"oom_kills": 1, "psi_cpu_some_avg10": 37.5},
	}
	for name, want := range map[string]float64{
		"cpu": 12, "memory": 40, "oom_kills": 1, "psi_cpu_some_avg10": 37.5,
	} {
		value, ok := metricValue(name, sample, domain.NetworkRate{})
		if !ok || value != want {
			t.Errorf("metricValue(%q) = %v, %v; want %v", name, value, ok, want)
		}
	}
	// A name nothing reported is still unknown rather than zero: a rule on a
	// reading this host does not take must not read as "0 and therefore fine".
	if _, ok := metricValue("swap_used_percent", sample, domain.NetworkRate{}); ok {
		t.Error("a reading that was never taken answered as a number")
	}
	// The rates stay derived from counters rather than stored.
	rate := domain.NetworkRate{Rx: 1024}
	if value, ok := metricValue("network_rx_rate", sample, rate); !ok || value != 1024 {
		t.Errorf("network_rx_rate = %v, %v", value, ok)
	}
}

// The map is written by whatever the agent sends and lands in every stored
// sample, so a misbehaving agent should cost one rejected sample rather than
// an unbounded row repeated every ten seconds.
func TestNamedReadingsAreBounded(t *testing.T) {
	now := time.Now().UTC()
	ok := domain.MetricSample{Values: map[string]float64{"oom_kills": 3}}
	if err := validateMetricSample(ok, now); err != nil {
		t.Fatalf("a plain named reading was refused: %v", err)
	}

	tooMany := map[string]float64{}
	for i := range metricValueLimit + 1 {
		tooMany["reading_"+strings.Repeat("x", i%5)+string(rune('a'+i%26))+string(rune('0'+i%10))] = 1
	}
	if err := validateMetricSample(domain.MetricSample{Values: tooMany}, now); err == nil {
		t.Error("a sample with more readings than the cap was accepted")
	}

	for _, name := range []string{
		"", "Uppercase", "has space", "has-dash", "9leading", "trailing.dot",
		strings.Repeat("a", metricNameMaxLength+1),
	} {
		if err := validateMetricSample(domain.MetricSample{Values: map[string]float64{name: 1}}, now); err == nil {
			t.Errorf("metric name %q was accepted", name)
		}
	}

	// A rule compares a number. NaN compares false against everything and an
	// infinity compares true against everything, so neither is actionable.
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if err := validateMetricSample(domain.MetricSample{Values: map[string]float64{"reading": value}}, now); err == nil {
			t.Errorf("%v was accepted as a reading", value)
		}
	}
}

// A counter is not a share, so the ceiling that keeps a percentage honest
// must not be applied to it: "held off the CPU for four million microseconds"
// is an ordinary number.
func TestARuleOnACounterIsNotCappedAtAHundred(t *testing.T) {
	base := domain.AlertRule{Name: "oom", Operator: ">", Duration: "1m", Severity: "critical"}

	rule := base
	rule.Metric, rule.Threshold = "oom_kills", 0
	if err := validateAlertRule(rule); err != nil {
		t.Fatalf("a rule on OOM kills was refused: %v", err)
	}
	rule.Metric, rule.Threshold = "throttled_usec", 4_000_000
	if err := validateAlertRule(rule); err != nil {
		t.Fatalf("a counter threshold above 100 was refused: %v", err)
	}

	// A share still cannot exceed what a share can be.
	rule.Metric, rule.Threshold = "cpu", 150
	if err := validateAlertRule(rule); err == nil {
		t.Error("a CPU rule above 100% was accepted")
	}
	// And a name nothing reports is still refused, so a typo fails at the
	// point it is written rather than silently never firing.
	rule.Metric, rule.Threshold = "cpu_usage", 50
	if err := validateAlertRule(rule); err == nil {
		t.Error("an unknown metric was accepted")
	}
}
