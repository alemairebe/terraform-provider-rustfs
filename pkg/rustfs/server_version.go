// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Version is a parsed RustFS server version.
//
// RustFS does not report one canonical format: GET /v3/healthinfo answers
// e.g. "refs/tags/1.0.0-rc.5" while GET /v3/info answers
// "2026-09-01T17:35:49Z@1.0.0-rc.5". Raw keeps the string exactly as the
// server reported it so diagnostics can quote the server verbatim.
type Version struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease string
	Raw        string
}

// ParseServerVersion parses the version strings RustFS reports. The last
// '@'-separated segment wins (GET /v3/info prefixes a build timestamp) and a
// "refs/tags/" or "v" prefix is stripped.
func ParseServerVersion(raw string) (Version, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Version{}, errors.New("empty server version")
	}

	candidate := trimmed
	if at := strings.LastIndex(candidate, "@"); at >= 0 {
		candidate = candidate[at+1:]
	}
	candidate = strings.TrimPrefix(candidate, "refs/tags/")
	candidate = strings.TrimPrefix(candidate, "v")

	core, prerelease := candidate, ""
	if i := strings.IndexByte(candidate, '-'); i >= 0 {
		core, prerelease = candidate[:i], candidate[i+1:]
	}
	// Build metadata ("+...") carries no ordering information.
	if i := strings.IndexByte(core, '+'); i >= 0 {
		core = core[:i]
	}
	if i := strings.IndexByte(prerelease, '+'); i >= 0 {
		prerelease = prerelease[:i]
	}

	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("unparseable server version %q: expected MAJOR.MINOR.PATCH", raw)
	}
	numbers := make([]int, 3)
	for i, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return Version{}, fmt.Errorf("unparseable server version %q: %q is not a number", raw, part)
		}
		numbers[i] = number
	}

	return Version{
		Major:      numbers[0],
		Minor:      numbers[1],
		Patch:      numbers[2],
		Prerelease: prerelease,
		Raw:        trimmed,
	}, nil
}

// Compare returns -1, 0 or 1 when v is older than, equal to or newer than
// other. A prerelease sorts before the matching release (1.0.0-rc.5 < 1.0.0),
// which is the precedence rule capability gating relies on.
func (v Version) Compare(other Version) int {
	for _, pair := range [][2]int{
		{v.Major, other.Major},
		{v.Minor, other.Minor},
		{v.Patch, other.Patch},
	} {
		switch {
		case pair[0] < pair[1]:
			return -1
		case pair[0] > pair[1]:
			return 1
		}
	}

	switch {
	case v.Prerelease == other.Prerelease:
		return 0
	case v.Prerelease == "":
		return 1
	case other.Prerelease == "":
		return -1
	default:
		return strings.Compare(v.Prerelease, other.Prerelease)
	}
}

// AtLeast reports whether v is other or newer.
func (v Version) AtLeast(other Version) bool {
	return v.Compare(other) >= 0
}

// String returns the dotted version, including the prerelease when present.
func (v Version) String() string {
	version := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Prerelease != "" {
		version += "-" + v.Prerelease
	}
	return version
}

// Feature names a RustFS server capability the provider depends on.
type Feature string

const (
	// FeatureMetrics is the admin metrics stream read by the rustfs_metrics
	// data source (GET /rustfs/admin/v3/metrics).
	FeatureMetrics Feature = "metrics"

	// FeatureTableBucket is the disable half of the S3 Tables catalog switch
	// (DELETE /iceberg/v1/buckets/{bucket}). Enable and read work on every
	// supported release; only the disable route is missing on 1.0.1.
	FeatureTableBucket Feature = "the S3 Tables catalog switch"
)

// featureSpec records what a feature needs from the server.
type featureSpec struct {
	// route is the admin API route that serves the feature.
	route string
	// minVersion is the first RustFS release that implements the route. nil
	// means no released server implements it yet.
	minVersion *Version
}

// featureSpecs is the capability matrix the provider reasons about. Add an
// entry only for a capability the provider actually uses.
var featureSpecs = map[Feature]featureSpec{
	FeatureMetrics: {
		route: "GET /rustfs/admin/v3/metrics",
		// No release implements the route yet: RustFS registers it and answers
		// 501 NotImplemented (observed on 1.0.1, 1.0.2 and 1.0.3).
	},
	FeatureTableBucket: {
		route: "DELETE /iceberg/v1/buckets/{bucket}",
		// Probed live: 1.0.1 answers 501 NotImplemented for the disable route,
		// while 1.0.2 and 1.0.3 disable the catalog. Enable and read work on
		// all three.
		minVersion: &Version{Major: 1, Minor: 0, Patch: 2, Raw: "1.0.2"},
	},
}

// Route returns the admin API route that serves the feature.
func (f Feature) Route() string {
	return featureSpecs[f].route
}

// UnsupportedFeatureError reports that the server does not implement the route
// a provider capability needs.
//
// RustFS registers admin routes before it implements them and answers
// 501 NotImplemented instead of 404, so route presence in the server's route
// table is not proof of support. The error carries the feature, its route and
// the version the server reported instead of the raw XML payload.
type UnsupportedFeatureError struct {
	Feature Feature
	Route   string
	// ServerVersion is the version the server reported, normalized to
	// MAJOR.MINOR.PATCH when it could be parsed. It is empty when the version
	// could not be determined.
	ServerVersion string
	MinVersion    *Version
	Err           error
}

func (e *UnsupportedFeatureError) Error() string {
	server := "the RustFS server"
	if e.ServerVersion != "" {
		server = "RustFS " + e.ServerVersion
	}

	route := e.Route
	if route == "" {
		route = e.Feature.Route()
	}
	if route == "" {
		route = string(e.Feature)
	}

	if e.MinVersion != nil {
		return fmt.Sprintf(
			"%s does not implement %s: %s is not available on this server; it requires RustFS %s or later",
			server, route, e.Feature, e.MinVersion,
		)
	}
	return fmt.Sprintf("%s does not implement %s: %s is not available on this server", server, route, e.Feature)
}

// Unwrap exposes the underlying API error so callers can still inspect the
// response status and payload.
func (e *UnsupportedFeatureError) Unwrap() error {
	return e.Err
}

// IsUnsupportedFeature reports whether err is an *UnsupportedFeatureError.
func IsUnsupportedFeature(err error) bool {
	var unsupported *UnsupportedFeatureError
	return errors.As(err, &unsupported)
}

// ServerVersion returns the version RustFS reports on GET /v3/healthinfo, both
// parsed and exactly as reported. The raw string is returned even when it
// cannot be parsed, so diagnostics can quote the server verbatim.
func (c *RustfsAdmin) ServerVersion() (Version, string, error) {
	info, err := c.GetHealthInfo()
	if err != nil {
		return Version{}, "", err
	}

	raw := strings.TrimSpace(info.Version)
	if raw == "" {
		return Version{}, "", errors.New("the server did not report a version")
	}

	version, err := ParseServerVersion(raw)
	return version, raw, err
}

// isUnsupportedRoute reports whether err is the server's answer for a route it
// does not implement. RustFS replies 501 NotImplemented for routes it registers
// without implementing them; 404 and 405 cover routes that were never
// registered. A 404 raised by an implemented route for a missing object is only
// ever seen by callers that pass it here, which feature-gated operations do not.
func isUnsupportedRoute(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}

	switch apiErr.StatusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return true
	}
	return strings.EqualFold(apiErr.Code, "NotImplemented")
}

// featureError converts the server's "route not implemented" answer into an
// *UnsupportedFeatureError carrying the feature, the route and the server
// version. Every other error is returned unchanged. The version lookup is best
// effort: the capability gap is already established without it.
func (c *RustfsAdmin) featureError(err error, feature Feature) error {
	if err == nil || !isUnsupportedRoute(err) {
		return err
	}

	unsupported := &UnsupportedFeatureError{
		Feature:    feature,
		Route:      feature.Route(),
		MinVersion: featureSpecs[feature].minVersion,
		Err:        err,
	}
	if _, raw, _ := c.ServerVersion(); raw != "" {
		unsupported.ServerVersion = raw
		if version, parseErr := ParseServerVersion(raw); parseErr == nil {
			unsupported.ServerVersion = version.String()
		}
	}
	return unsupported
}
