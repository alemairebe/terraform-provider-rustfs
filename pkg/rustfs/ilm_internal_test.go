// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import "testing"

func TestReadAllToleratesAMissingBody(t *testing.T) {
	body, err := readAll(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body != nil {
		t.Fatalf("expected no body, got %q", body)
	}
}
