// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package recommendation provides a resource recommendation engine that
// analyzes deployment resource configurations and SLO targets to suggest
// right-sizing for CPU, memory, and replica counts.
package recommendation

import (
	"fmt"
	"strings"
)

// Direction indicates whether a resource should be increased, decreased,
// or kept at its current level.
type Direction string

const (
	DirectionIncrease Direction = "increase"
	DirectionDecrease Direction = "decrease"
	DirectionKeep     Direction = "keep"
)

// ResourceRecommendation describes a suggested change to a resource
// configuration.
type ResourceRecommendation struct {
	Resource   string    `json:"resource"`
	Current    string    `json:"current"`
	Suggested  string    `json:"suggested"`
	Direction  Direction `json:"direction"`
	Reason     string    `json:"reason"`
	Confidence float64   `json:"confidence"`
}

// Input is the data the recommendation engine needs to generate suggestions.
type Input struct {
	// CPURequest, MemoryRequest, CPULimit, MemoryLimit are the current
	// container resource configurations.
	CPURequest    string
	MemoryRequest string
	CPULimit      string
	MemoryLimit   string

	// Replicas is the current desired replica count.
	Replicas int

	// MaxReplicas is the HPA maximum replica count.
	MaxReplicas int

	// SLOTarget is the SLO availability target (e.g., 0.999 for 99.9%).
	SLOTarget float64

	// AvgCPUUtilization is the average CPU utilization percentage (0-100).
	AvgCPUUtilization float64

	// AvgMemoryUtilization is the average memory utilization percentage (0-100).
	AvgMemoryUtilization float64

	// P99LatencyMs is the current p99 latency in milliseconds.
	P99LatencyMs float64

	// SLOLatencyTargetMs is the SLO latency target in milliseconds.
	SLOLatencyTargetMs float64

	// ErrorRate is the current error rate as a fraction (0-1).
	ErrorRate float64

	// SLOErrorRateTarget is the SLO error rate target as a fraction.
	SLOErrorRateTarget float64
}

// Engine generates resource recommendations based on utilization metrics
// and SLO targets.
type Engine struct{}

// NewEngine returns a recommendation engine.
func NewEngine() *Engine {
	return &Engine{}
}

// Recommend analyzes the input and returns resource recommendations.
func (e Engine) Recommend(input Input) []ResourceRecommendation {
	var recs []ResourceRecommendation

	recs = append(recs, recommendCPU(input))
	recs = append(recs, recommendMemory(input))
	recs = append(recs, recommendReplicas(input))

	return recs
}

func recommendCPU(input Input) ResourceRecommendation {
	rec := ResourceRecommendation{
		Resource:  "cpu_request",
		Current:   input.CPURequest,
		Suggested: input.CPURequest,
		Direction: DirectionKeep,
	}

	if input.AvgCPUUtilization > 80 {
		rec.Direction = DirectionIncrease
		rec.Suggested = increaseCPU(input.CPURequest)
		rec.Reason = fmt.Sprintf("CPU utilization %.0f%% is above 80%% threshold; consider increasing request to avoid throttling", input.AvgCPUUtilization)
		rec.Confidence = 0.8
	} else if input.AvgCPUUtilization < 30 && input.CPURequest != "" {
		rec.Direction = DirectionDecrease
		rec.Suggested = decreaseCPU(input.CPURequest)
		rec.Reason = fmt.Sprintf("CPU utilization %.0f%% is below 30%%; consider decreasing request to reduce cost", input.AvgCPUUtilization)
		rec.Confidence = 0.6
	} else {
		rec.Reason = fmt.Sprintf("CPU utilization %.0f%% is within healthy range", input.AvgCPUUtilization)
		rec.Confidence = 0.9
	}

	return rec
}

func recommendMemory(input Input) ResourceRecommendation {
	rec := ResourceRecommendation{
		Resource:  "memory_request",
		Current:   input.MemoryRequest,
		Suggested: input.MemoryRequest,
		Direction: DirectionKeep,
	}

	if input.AvgMemoryUtilization > 85 {
		rec.Direction = DirectionIncrease
		rec.Suggested = increaseMemory(input.MemoryRequest)
		rec.Reason = fmt.Sprintf("Memory utilization %.0f%% is above 85%% threshold; OOM risk is high", input.AvgMemoryUtilization)
		rec.Confidence = 0.85
	} else if input.AvgMemoryUtilization < 40 && input.MemoryRequest != "" {
		rec.Direction = DirectionDecrease
		rec.Suggested = decreaseMemory(input.MemoryRequest)
		rec.Reason = fmt.Sprintf("Memory utilization %.0f%% is below 40%%; consider decreasing request to reduce cost", input.AvgMemoryUtilization)
		rec.Confidence = 0.5
	} else {
		rec.Reason = fmt.Sprintf("Memory utilization %.0f%% is within healthy range", input.AvgMemoryUtilization)
		rec.Confidence = 0.9
	}

	return rec
}

func recommendReplicas(input Input) ResourceRecommendation {
	rec := ResourceRecommendation{
		Resource:  "replicas",
		Current:   fmt.Sprintf("%d", input.Replicas),
		Suggested: fmt.Sprintf("%d", input.Replicas),
		Direction: DirectionKeep,
	}

	if input.ErrorRate > input.SLOErrorRateTarget && input.SLOErrorRateTarget > 0 {
		rec.Direction = DirectionIncrease
		rec.Suggested = fmt.Sprintf("%d", input.Replicas+1)
		rec.Reason = fmt.Sprintf("Error rate %.4f exceeds SLO target %.4f; adding replicas may improve reliability", input.ErrorRate, input.SLOErrorRateTarget)
		rec.Confidence = 0.6
	} else if input.P99LatencyMs > input.SLOLatencyTargetMs && input.SLOLatencyTargetMs > 0 {
		rec.Direction = DirectionIncrease
		rec.Suggested = fmt.Sprintf("%d", input.Replicas+1)
		rec.Reason = fmt.Sprintf("P99 latency %.0fms exceeds SLO target %.0fms; adding replicas may reduce latency", input.P99LatencyMs, input.SLOLatencyTargetMs)
		rec.Confidence = 0.65
	} else if input.AvgCPUUtilization < 20 && input.Replicas > 2 {
		rec.Direction = DirectionDecrease
		rec.Suggested = fmt.Sprintf("%d", input.Replicas-1)
		rec.Reason = fmt.Sprintf("CPU utilization %.0f%% is very low with %d replicas; consider reducing to save cost", input.AvgCPUUtilization, input.Replicas)
		rec.Confidence = 0.5
	} else {
		rec.Reason = fmt.Sprintf("Replica count %d is appropriate for current load and SLO targets", input.Replicas)
		rec.Confidence = 0.85
	}

	return rec
}

// increaseCPU returns a CPU request roughly 50% larger than the input.
// It handles common formats like "100m", "250m", "1", "2".
func increaseCPU(current string) string {
	if current == "" {
		return "250m"
	}
	if strings.HasSuffix(current, "m") {
		var millicores int
		fmt.Sscanf(current, "%dm", &millicores)
		return fmt.Sprintf("%dm", int(float64(millicores)*1.5))
	}
	var cores float64
	fmt.Sscanf(current, "%f", &cores)
	return fmt.Sprintf("%.1f", cores*1.5)
}

// decreaseCPU returns a CPU request roughly 33% smaller than the input.
func decreaseCPU(current string) string {
	if current == "" {
		return "100m"
	}
	if strings.HasSuffix(current, "m") {
		var millicores int
		fmt.Sscanf(current, "%dm", &millicores)
		decreased := int(float64(millicores) * 0.67)
		if decreased < 10 {
			decreased = 10
		}
		return fmt.Sprintf("%dm", decreased)
	}
	var cores float64
	fmt.Sscanf(current, "%f", &cores)
	return fmt.Sprintf("%.1f", cores*0.67)
}

// increaseMemory returns a memory request roughly 50% larger.
// It handles common formats like "128Mi", "256Mi", "1Gi".
func increaseMemory(current string) string {
	if current == "" {
		return "256Mi"
	}
	if strings.HasSuffix(current, "Mi") {
		var mi int
		fmt.Sscanf(current, "%dMi", &mi)
		return fmt.Sprintf("%dMi", int(float64(mi)*1.5))
	}
	if strings.HasSuffix(current, "Gi") {
		var gi int
		fmt.Sscanf(current, "%dGi", &gi)
		return fmt.Sprintf("%dGi", int(float64(gi)*1.5))
	}
	return current
}

// decreaseMemory returns a memory request roughly 33% smaller.
func decreaseMemory(current string) string {
	if current == "" {
		return "128Mi"
	}
	if strings.HasSuffix(current, "Mi") {
		var mi int
		fmt.Sscanf(current, "%dMi", &mi)
		decreased := int(float64(mi) * 0.67)
		if decreased < 16 {
			decreased = 16
		}
		return fmt.Sprintf("%dMi", decreased)
	}
	if strings.HasSuffix(current, "Gi") {
		var gi int
		fmt.Sscanf(current, "%dGi", &gi)
		return fmt.Sprintf("%dGi", int(float64(gi)*0.67))
	}
	return current
}
