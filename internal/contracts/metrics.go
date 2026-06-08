package contracts

import "time"

type MetricSample struct {
	Name   string            `json:"name"`
	Value  float64           `json:"value"`
	Unit   string            `json:"unit,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

type ComponentMetrics struct {
	Component   string         `json:"component"`
	Version     string         `json:"version"`
	CollectedAt string         `json:"collected_at"`
	Samples     []MetricSample `json:"samples"`
}

func NewComponentMetrics(component string, samples []MetricSample) ComponentMetrics {
	if samples == nil {
		samples = []MetricSample{}
	}
	return ComponentMetrics{
		Component:   component,
		Version:     "v1",
		CollectedAt: time.Now().UTC().Format(time.RFC3339),
		Samples:     samples,
	}
}

func CountMetric(name string, value int, labels map[string]string) MetricSample {
	return MetricSample{Name: name, Value: float64(value), Unit: "count", Labels: labels}
}

func GaugeMetric(name string, value float64, unit string, labels map[string]string) MetricSample {
	return MetricSample{Name: name, Value: value, Unit: unit, Labels: labels}
}
