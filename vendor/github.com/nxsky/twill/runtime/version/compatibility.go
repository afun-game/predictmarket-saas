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

package version

import (
	"fmt"
	"sort"
	"strings"
)

// StabilityLevel classifies the stability guarantee of an API.
type StabilityLevel string

const (
	StabilityExperimental StabilityLevel = "experimental"
	StabilityBeta         StabilityLevel = "beta"
	StabilityStable       StabilityLevel = "stable"
	StabilityDeprecated   StabilityLevel = "deprecated"
)

// CompatVer describes a semantic version with stability metadata for
// compatibility policy evaluation. It is distinct from the existing
// SemVer type which is used for deployer and codegen API versioning.
type CompatVer struct {
	Major     int            `json:"major"`
	Minor     int            `json:"minor"`
	Patch     int            `json:"patch"`
	Pre       string         `json:"pre,omitempty"`
	Stability StabilityLevel `json:"stability"`
}

// String returns the version string representation.
func (v CompatVer) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// ParseCompatVer parses a semantic version string into a CompatVer.
func ParseCompatVer(s string) (CompatVer, error) {
	pre := ""
	if idx := strings.Index(s, "-"); idx >= 0 {
		pre = s[idx+1:]
		s = s[:idx]
	}
	var major, minor, patch int
	if n, err := fmt.Sscanf(s, "%d.%d.%d", &major, &minor, &patch); n != 3 || err != nil {
		return CompatVer{}, fmt.Errorf("invalid version %q: %w", s, err)
	}
	stability := StabilityStable
	if pre != "" {
		stability = StabilityBeta
		if strings.HasPrefix(pre, "alpha") || strings.HasPrefix(pre, "dev") {
			stability = StabilityExperimental
		}
	}
	return CompatVer{
		Major:     major,
		Minor:     minor,
		Patch:     patch,
		Pre:       pre,
		Stability: stability,
	}, nil
}

// IsCompatibleWith checks whether this version is compatible with the
// required version according to the compatibility policy.
func (v CompatVer) IsCompatibleWith(required CompatVer) bool {
	if v.Stability == StabilityExperimental || required.Stability == StabilityExperimental {
		return v.Major == required.Major && v.Minor == required.Minor && v.Patch == required.Patch
	}
	if v.Stability == StabilityBeta || required.Stability == StabilityBeta {
		return v.Major == required.Major && v.Minor == required.Minor
	}
	return v.Major == required.Major
}

// IsBreakingChangeFrom checks whether upgrading from `from` to this
// version is a breaking change.
func (v CompatVer) IsBreakingChangeFrom(from CompatVer) bool {
	if v.Major != from.Major {
		return true
	}
	if (v.Stability == StabilityBeta || from.Stability == StabilityBeta) && v.Minor != from.Minor {
		return true
	}
	return false
}

// ReleaseTrain describes a scheduled release train for the platform.
type ReleaseTrain struct {
	Name      string         `json:"name"`
	Version   CompatVer      `json:"version"`
	Schedule  string         `json:"schedule"`
	Stability StabilityLevel `json:"stability"`
	Features  []string       `json:"features,omitempty"`
	Fixes     []string       `json:"fixes,omitempty"`
	Breaking  bool           `json:"breaking,omitempty"`
}

// VersionRange describes a range of supported versions.
type VersionRange struct {
	MinVersion CompatVer      `json:"min_version"`
	MaxVersion CompatVer      `json:"max_version,omitempty"`
	Stability  StabilityLevel `json:"stability"`
}

// CompatibilityPolicy describes the platform's versioning and compatibility
// guarantees.
type CompatibilityPolicy struct {
	CurrentVersion    SemVer         `json:"current_version"`
	SupportedRanges   []VersionRange `json:"supported_ranges"`
	ReleaseTrains     []ReleaseTrain `json:"release_trains"`
	DeprecationPolicy string         `json:"deprecation_policy"`
}

// DefaultCompatibilityPolicy returns the default compatibility policy.
func DefaultCompatibilityPolicy() CompatibilityPolicy {
	return CompatibilityPolicy{
		CurrentVersion: SemVer{Major: DeployerMajor, Minor: DeployerMinor, Patch: 0},
		SupportedRanges: []VersionRange{
			{MinVersion: CompatVer{Major: 0, Minor: 24, Patch: 0, Stability: StabilityDeprecated}, Stability: StabilityDeprecated},
		},
		ReleaseTrains: []ReleaseTrain{
			{Name: "v0.24", Version: CompatVer{Major: 0, Minor: 24, Patch: 0, Stability: StabilityStable}, Schedule: "quarterly", Stability: StabilityStable},
		},
		DeprecationPolicy: "Deprecated APIs are supported for one major version cycle before removal. Breaking changes require a major version bump.",
	}
}

// CheckCompatVer verifies that the given version is within the policy's
// supported ranges.
func (p CompatibilityPolicy) CheckCompatVer(v CompatVer) error {
	for _, rng := range p.SupportedRanges {
		if v.IsCompatibleWith(rng.MinVersion) {
			return nil
		}
	}
	return fmt.Errorf("version %s is not within any supported range", v.String())
}

// NextReleaseTrain returns the next scheduled release train.
func (p CompatibilityPolicy) NextReleaseTrain() *ReleaseTrain {
	if len(p.ReleaseTrains) == 0 {
		return nil
	}
	trains := make([]ReleaseTrain, len(p.ReleaseTrains))
	copy(trains, p.ReleaseTrains)
	sort.Slice(trains, func(i, j int) bool {
		if trains[i].Version.Major != trains[j].Version.Major {
			return trains[i].Version.Major > trains[j].Version.Major
		}
		return trains[i].Version.Minor > trains[j].Version.Minor
	})
	return &trains[0]
}
