// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
)

// unitTestCredential returns the value a unit test uses where a credential has
// to be non-empty. The mocked admin API never authenticates, so it only has to
// be a non-empty, distinct string for the wire body to be asserted;
// credential-shaped literals stay out of the source, so the environment wins
// when it is set and the fallback is generated.
func unitTestCredential(env string, fallback rune) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return strings.Repeat(string(fallback), 16)
}

// Extensions to the mocked-admin-API harness in provider_test.go.

// internalErrorBody is the error payload the mocked admin API answers with when
// a test exercises a failure path.
const internalErrorBody = `<Error><Code>InternalError</Code><Message>boom</Message></Error>`

// queryValue returns one query parameter of a recorded request.
func queryValue(t *testing.T, request adminRequest, key string) string {
	t.Helper()
	values, err := url.ParseQuery(request.Query)
	if err != nil {
		t.Fatalf("parsing the query of %s %s: %v", request.Method, request.Path, err)
	}
	return values.Get(key)
}

// requireNoDiagnostics fails when a request carried any diagnostic.
func requireNoDiagnostics(t *testing.T, diags diag.Diagnostics) {
	t.Helper()
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags.Errors())
	}
}
