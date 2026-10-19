// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestGroupResourceSchema(t *testing.T) {
	r := NewGroupResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.TODO(), resource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	if _, ok := attrs["name"]; !ok {
		t.Error("expected name attribute")
	}
	if _, ok := attrs["status"]; !ok {
		t.Error("expected status attribute")
	}
	if _, ok := attrs["members"]; !ok {
		t.Error("expected members attribute")
	}
}

func TestGroupResourceMetadata(t *testing.T) {
	r := NewGroupResource()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.TODO(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_group" {
		t.Errorf("expected rustfs_group, got %s", resp.TypeName)
	}
}

// TestGroupResourceLifecyclePaths covers the paths the acceptance suite cannot
// reach with a live server: a group that disappeared behind Terraform's back, a
// delete that arrives after the group is already gone, and every failing API
// call in Create, Read and Delete.
func TestGroupResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			// RustFS answers 404 NoSuchResource for a group it does not have.
			name:        "missing group is removed from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "group 'tf-test' does not exist")}},
			wantRemoved: true,
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading group",
			wantDetail:  "Could not read group: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewGroupResource(), client)

			resp := h.read(&GroupResourceModel{Name: types.StringValue("tf-test"), Members: nullStringSet()})

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertStatePresent(t, resp.State)
		})
	}

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			name:   "already deleted group is not an error",
			routes: []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "group 'tf-test' does not exist")}},
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error deleting group",
			wantDetail:  "Could not delete group: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewGroupResource(), client)

			resp := h.delete(&GroupResourceModel{Name: types.StringValue("tf-test"), Members: nullStringSet()})

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}

	createCases := []struct {
		name        string
		routes      []requestRoute
		window      *GroupResourceModel
		wantSummary string
		wantDetail  string
	}{
		{
			// A member that does not exist is rejected by update-group-members.
			name:        "rejected members report a diagnostic and write no state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "user 'ghost' does not exist")}},
			window:      &GroupResourceModel{Name: types.StringValue("tf-test"), Members: stringSetFromSlice([]string{"ghost"})},
			wantSummary: "Error creating group",
			wantDetail:  "user 'ghost' does not exist",
		},
		{
			name: "failed status change is reported",
			routes: []requestRoute{
				{method: http.MethodPut, pathContains: "update-group-members", status: http.StatusOK},
				{method: http.MethodPut, pathContains: "set-group-status", status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "status exploded")},
			},
			window:      &GroupResourceModel{Name: types.StringValue("tf-test"), Status: types.StringValue("disabled"), Members: nullStringSet()},
			wantSummary: "Error setting group status",
			wantDetail:  "status exploded",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewGroupResource(), client)

			resp := h.create(tc.window)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, tc.window)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewGroupResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
