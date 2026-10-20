// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBucketOnDemandMigrationStatusSchema(t *testing.T) {
	d := NewBucketOnDemandMigrationStatusDataSource()
	resp := &datasource.SchemaResponse{}
	d.Schema(context.Background(), datasource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("schema is not a valid implementation: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{
		"bucket", "configured", "enabled", "module_enabled", "source_provider", "endpoint_host",
		"breaker", "counters", "last_source_error", "inflight_pulls", "queue_depth",
		"served_by_source_ratio", "updated_at", "backfill",
	} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("missing schema attribute %q", name)
		}
	}

	bucket, ok := attrs["bucket"].(schema.StringAttribute)
	if !ok || !bucket.Required {
		t.Error("bucket must be required")
	}

	counters, ok := attrs["counters"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("counters is %T", attrs["counters"])
	}
	requests, ok := counters.Attributes["requests_total"].(schema.MapAttribute)
	if !ok {
		t.Fatalf("counters.requests_total is %T", counters.Attributes["requests_total"])
	}
	if _, ok := requests.ElementType.(types.MapType); !ok {
		t.Errorf("counters.requests_total element type = %T, want a map of counts", requests.ElementType)
	}

	backfill, ok := attrs["backfill"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("backfill is %T", attrs["backfill"])
	}
	for _, name := range []string{"job_id", "state", "listed", "enqueued", "pulled", "skipped_existing", "failed", "bytes", "updated_at"} {
		if _, ok := backfill.Attributes[name]; !ok {
			t.Errorf("backfill is missing %q", name)
		}
	}
}

func TestBucketOnDemandMigrationStatusMetadata(t *testing.T) {
	d := NewBucketOnDemandMigrationStatusDataSource()
	resp := &datasource.MetadataResponse{}
	d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_bucket_on_demand_migration_status" {
		t.Errorf("expected rustfs_bucket_on_demand_migration_status, got %s", resp.TypeName)
	}
}

func TestApplyODMStatusMapsEveryField(t *testing.T) {
	ratio := 0.25
	opened := "2026-10-20T09:30:00Z"
	provider := "minio"
	host := "legacy.example.com"
	updated := "2026-10-20T10:00:00Z"

	status := rustfs.ODMStatus{
		Configured:    true,
		Enabled:       true,
		ModuleEnabled: true,
		Provider:      &provider,
		EndpointHost:  &host,
		Breaker:       &rustfs.ODMBreakerStatus{State: "open", OpenedAt: &opened},
		Counters: &rustfs.ODMCounters{
			RequestsTotal:      map[string]map[string]uint64{"GET": {"source_hit": 3}},
			PulledBytesTotal:   1234,
			PulledObjectsTotal: map[string]uint64{"source_hit": 3},
			PullFailuresTotal:  map[string]uint64{"source_timeout": 1},
			SourceLatency: rustfs.ODMSourceLatency{
				Buckets: []rustfs.ODMLatencyBucket{{LeMS: 100, Count: 2}},
				Count:   3,
				SumMS:   700,
			},
		},
		LastSourceError:     &rustfs.ODMLastSourceError{Class: "access_denied", At: "2026-10-20T09:00:00Z"},
		InflightPulls:       2,
		QueueDepth:          4,
		ServedBySourceRatio: &ratio,
		UpdatedAt:           &updated,
		Backfill: &rustfs.ODMBackfillSummary{
			JobID: "job-1", State: "running", Listed: 10, Enqueued: 8, Pulled: 5,
			SkippedExisting: 2, Failed: 1, Bytes: 4096, UpdatedAt: "2026-10-20T10:05:00Z",
		},
	}

	model := &bucketOnDemandMigrationStatusModel{Bucket: types.StringValue("photos")}
	diags := applyODMStatus(context.Background(), model, status)
	if diags.HasError() {
		t.Fatalf("apply status: %v", diags)
	}

	if !model.Configured.ValueBool() || !model.Enabled.ValueBool() || !model.ModuleEnabled.ValueBool() {
		t.Error("configured/enabled/module_enabled were not mapped")
	}
	if model.SourceProvider.ValueString() != "minio" || model.EndpointHost.ValueString() != "legacy.example.com" {
		t.Errorf("source_provider/endpoint_host = %q/%q", model.SourceProvider.ValueString(), model.EndpointHost.ValueString())
	}
	if model.Breaker == nil || model.Breaker.State.ValueString() != "open" || model.Breaker.OpenedAt.ValueString() != opened {
		t.Errorf("breaker = %+v", model.Breaker)
	}
	if model.Counters == nil {
		t.Fatal("counters = nil")
	}
	var requests map[string]map[string]int64
	if d := model.Counters.RequestsTotal.ElementsAs(context.Background(), &requests, false); d.HasError() {
		t.Fatalf("requests_total: %v", d)
	}
	if got := requests["GET"]["source_hit"]; got != 3 {
		t.Errorf("requests_total[GET][source_hit] = %d, want 3", got)
	}
	if model.Counters.PulledBytesTotal.ValueInt64() != 1234 {
		t.Errorf("pulled_bytes_total = %d", model.Counters.PulledBytesTotal.ValueInt64())
	}
	if model.Counters.SourceLatency == nil || model.Counters.SourceLatency.SumMS.ValueInt64() != 700 {
		t.Errorf("source_latency = %+v", model.Counters.SourceLatency)
	}
	if model.LastSourceError == nil || model.LastSourceError.Class.ValueString() != "access_denied" {
		t.Errorf("last_source_error = %+v", model.LastSourceError)
	}
	if model.InflightPulls.ValueInt64() != 2 || model.QueueDepth.ValueInt64() != 4 {
		t.Error("inflight_pulls/queue_depth were not mapped")
	}
	if model.ServedBySourceRatio.ValueFloat64() != 0.25 {
		t.Errorf("served_by_source_ratio = %v", model.ServedBySourceRatio.ValueFloat64())
	}
	if model.Backfill == nil || model.Backfill.JobID.ValueString() != "job-1" || model.Backfill.Bytes.ValueInt64() != 4096 {
		t.Errorf("backfill = %+v", model.Backfill)
	}
}

func TestApplyODMStatusLeavesRuntimeFieldsNull(t *testing.T) {
	model := &bucketOnDemandMigrationStatusModel{Bucket: types.StringValue("photos")}
	diags := applyODMStatus(context.Background(), model, rustfs.ODMStatus{})
	if diags.HasError() {
		t.Fatalf("apply empty status: %v", diags)
	}

	if model.Configured.ValueBool() || model.Enabled.ValueBool() {
		t.Error("an unconfigured bucket must not report configured/enabled")
	}
	if model.Breaker != nil || model.Counters != nil || model.LastSourceError != nil || model.Backfill != nil {
		t.Error("absent runtime blocks must stay null")
	}
	if !model.SourceProvider.IsNull() || !model.EndpointHost.IsNull() || !model.UpdatedAt.IsNull() {
		t.Error("absent config strings must stay null")
	}
	if !model.ServedBySourceRatio.IsNull() {
		t.Error("served_by_source_ratio must stay null when the server cannot compute it")
	}
}
