// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestBatchJobsDataSourceSchema(t *testing.T) {
	d := NewBatchJobsDataSource()
	resp := &datasource.SchemaResponse{}
	d.Schema(context.Background(), datasource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	for _, name := range []string{"job_type", "job_id", "id", "jobs", "job", "status", "found", "json"} {
		if _, ok := resp.Schema.Attributes[name]; !ok {
			t.Errorf("missing schema attribute %q", name)
		}
	}

	jobs, ok := resp.Schema.Attributes["jobs"].(datasourceschema.ListAttribute)
	if !ok {
		t.Fatalf("jobs is %T, want schema.ListAttribute", resp.Schema.Attributes["jobs"])
	}
	if jobs.ElementType != types.StringType {
		t.Errorf("jobs element type = %v, want string", jobs.ElementType)
	}

	meta := &datasource.MetadataResponse{}
	d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, meta)
	if meta.TypeName != "rustfs_batch_jobs" {
		t.Errorf("type name = %q", meta.TypeName)
	}
}

func batchJobsConfig() *BatchJobsDataSourceModel {
	return &BatchJobsDataSourceModel{
		JobType: types.StringNull(),
		JobID:   types.StringNull(),
		ID:      types.StringNull(),
		Jobs:    types.ListNull(types.StringType),
		Job:     types.StringNull(),
		Status:  types.StringNull(),
		Found:   types.BoolNull(),
		JSON:    types.StringNull(),
	}
}

func readBatchJobs(t *testing.T, d *BatchJobsDataSource, sch datasourceschema.Schema, model *BatchJobsDataSourceModel) *datasource.ReadResponse {
	t.Helper()

	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, model),
	}, resp)
	return resp
}

func TestBatchJobsDataSourceReadList(t *testing.T) {
	client, rec := routedClient(t, requestRoute{
		status: http.StatusOK,
		body:   `{"jobs":[{"ID":"job-1","Type":"replicate"}]}`,
	})
	d := NewBatchJobsDataSource().(*BatchJobsDataSource)
	d.client = client
	sch := dataSourceSchemaOf(t, d)

	resp := readBatchJobs(t, d, sch, batchJobsConfig())
	requireNoDiagnostics(t, resp.Diagnostics)
	assertStateString(t, resp.State, path.Root("id"), "batch-jobs")
	assertStateString(t, resp.State, path.Root("jobs").AtListIndex(0), `{"ID":"job-1","Type":"replicate"}`)
	rec.assertRequest(t, "GET /rustfs/admin/v3/list-jobs")

	var found types.Bool
	if diags := resp.State.GetAttribute(context.Background(), path.Root("found"), &found); diags.HasError() {
		t.Fatalf("found attribute: %v", diags)
	}
	if !found.IsNull() {
		t.Errorf("found = %v, want null when no job_id was given", found)
	}

	var raw types.String
	if diags := resp.State.GetAttribute(context.Background(), path.Root("json"), &raw); diags.HasError() {
		t.Fatalf("json attribute: %v", diags)
	}
	if !strings.Contains(raw.ValueString(), `"jobs"`) {
		t.Errorf("json does not carry the raw payload: %q", raw.ValueString())
	}
}

func TestBatchJobsDataSourceReadWithJobID(t *testing.T) {
	client, rec := routedClient(t,
		requestRoute{pathContains: "list-jobs", status: http.StatusOK, body: `{"jobs":[]}`},
		requestRoute{pathContains: "describe-job", status: http.StatusOK, body: `{"ID":"job-1","Type":"replicate"}`},
		requestRoute{pathContains: "status-job", status: http.StatusOK, body: `{"status":"failed"}`},
	)
	d := NewBatchJobsDataSource().(*BatchJobsDataSource)
	d.client = client
	sch := dataSourceSchemaOf(t, d)

	model := batchJobsConfig()
	model.JobID = types.StringValue("job-1")
	resp := readBatchJobs(t, d, sch, model)
	requireNoDiagnostics(t, resp.Diagnostics)
	assertStateString(t, resp.State, path.Root("id"), "batch-jobs/job-1")
	assertStateString(t, resp.State, path.Root("job"), `{"ID":"job-1","Type":"replicate"}`)
	assertStateString(t, resp.State, path.Root("status"), `{"status":"failed"}`)
	assertStateBool(t, resp.State, path.Root("found"), true)
	rec.assertRequest(t, "GET /rustfs/admin/v3/describe-job")
	rec.assertRequest(t, "GET /rustfs/admin/v3/status-job")
}

func TestBatchJobsDataSourceMissingJobIsNotAnError(t *testing.T) {
	client, rec := routedClient(t,
		requestRoute{pathContains: "list-jobs", status: http.StatusOK, body: `{"jobs":[]}`},
		requestRoute{pathContains: "describe-job", status: http.StatusNotFound,
			body: s3ErrorXML("NoSuchKey", "no such batch job: job-1")},
	)
	d := NewBatchJobsDataSource().(*BatchJobsDataSource)
	d.client = client
	sch := dataSourceSchemaOf(t, d)

	model := batchJobsConfig()
	model.JobID = types.StringValue("job-1")
	resp := readBatchJobs(t, d, sch, model)
	requireNoDiagnostics(t, resp.Diagnostics)
	assertStateBool(t, resp.State, path.Root("found"), false)
	rec.assertNoRequest(t, "status-job")
}

func TestBatchJobsDataSourceStatusDisappearedKeepsDescribedJob(t *testing.T) {
	client, _ := routedClient(t,
		requestRoute{pathContains: "list-jobs", status: http.StatusOK, body: `{"jobs":[]}`},
		requestRoute{pathContains: "describe-job", status: http.StatusOK, body: `{"ID":"job-1","Type":"replicate"}`},
		requestRoute{pathContains: "status-job", status: http.StatusNotFound,
			body: s3ErrorXML("NoSuchKey", "no such batch job: job-1")},
	)
	d := NewBatchJobsDataSource().(*BatchJobsDataSource)
	d.client = client
	sch := dataSourceSchemaOf(t, d)

	model := batchJobsConfig()
	model.JobID = types.StringValue("job-1")
	resp := readBatchJobs(t, d, sch, model)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error diagnostics: %v", resp.Diagnostics.Errors())
	}
	if resp.Diagnostics.WarningsCount() != 1 {
		t.Fatalf("warnings = %d, want 1: %v", resp.Diagnostics.WarningsCount(), resp.Diagnostics.Warnings())
	}
	assertStateBool(t, resp.State, path.Root("found"), true)
	assertStateString(t, resp.State, path.Root("job"), `{"ID":"job-1","Type":"replicate"}`)

	var status types.String
	if diags := resp.State.GetAttribute(context.Background(), path.Root("status"), &status); diags.HasError() {
		t.Fatalf("status attribute: %v", diags)
	}
	if !status.IsNull() {
		t.Errorf("status = %q, want null when status-job no longer knows the job", status.ValueString())
	}
}

func TestBatchJobsDataSourceUnsupportedRoute(t *testing.T) {
	client, _ := routedClient(t,
		requestRoute{pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.1"}`},
		requestRoute{status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")},
	)
	d := NewBatchJobsDataSource().(*BatchJobsDataSource)
	d.client = client

	resp := readBatchJobs(t, d, dataSourceSchemaOf(t, d), batchJobsConfig())
	assertErrorDiagnostic(t, resp.Diagnostics,
		"Batch jobs are not available on this RustFS server",
		"RustFS 1.0.1")
}

func TestBatchJobsDataSourceOtherError(t *testing.T) {
	client, _ := routedClient(t, requestRoute{
		status: http.StatusForbidden,
		body:   s3ErrorXML("AccessDenied", "Access Denied."),
	})
	d := NewBatchJobsDataSource().(*BatchJobsDataSource)
	d.client = client

	resp := readBatchJobs(t, d, dataSourceSchemaOf(t, d), batchJobsConfig())
	assertErrorDiagnostic(t, resp.Diagnostics, "Error reading batch jobs", "AccessDenied")
}

func TestAccBatchJobsDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
data "rustfs_batch_jobs" "test" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.rustfs_batch_jobs.test", "id", "batch-jobs"),
					resource.TestCheckResourceAttrSet("data.rustfs_batch_jobs.test", "json"),
					resource.TestCheckResourceAttr("data.rustfs_batch_jobs.test", "jobs.#", "0"),
				),
			},
		},
	})
}
