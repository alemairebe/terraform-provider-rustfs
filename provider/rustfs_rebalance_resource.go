// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ resource.Resource                = &RebalanceResource{}
	_ resource.ResourceWithConfigure   = &RebalanceResource{}
	_ resource.ResourceWithImportState = &RebalanceResource{}
)

// legacyRebalanceID is the fixed id older provider versions stored. It is
// adopted from the first successful status read so existing state files migrate
// to the server's trigger id without recreating the resource.
const legacyRebalanceID = "rebalance-triggered"

// RebalanceResource triggers a pool rebalance and observes it.
//
// Create starts a rebalance, Read mirrors GET /rebalance/status, and Delete
// stops an in-flight rebalance through POST /rebalance/stop. Stopping is not
// reversible: data already moved stays where it was moved.
type RebalanceResource struct {
	client *AllClient
}

type RebalanceResourceModel struct {
	ID              types.String `tfsdk:"id"`
	Status          types.String `tfsdk:"status"`
	StoppedAt       types.String `tfsdk:"stopped_at"`
	Pools           types.List   `tfsdk:"pools"`
	StopPropagation types.Object `tfsdk:"stop_propagation"`
}

type RebalancePoolModel struct {
	ID              types.Int64   `tfsdk:"id"`
	Status          types.String  `tfsdk:"status"`
	Stopping        types.Bool    `tfsdk:"stopping"`
	Used            types.Float64 `tfsdk:"used"`
	LastError       types.String  `tfsdk:"last_error"`
	CleanupWarnings types.Object  `tfsdk:"cleanup_warnings"`
	Progress        types.Object  `tfsdk:"progress"`
}

type RebalanceProgressModel struct {
	Objects          types.Int64  `tfsdk:"objects"`
	Versions         types.Int64  `tfsdk:"versions"`
	Bytes            types.Int64  `tfsdk:"bytes"`
	RemainingBuckets types.Int64  `tfsdk:"remaining_buckets"`
	Bucket           types.String `tfsdk:"bucket"`
	Object           types.String `tfsdk:"object"`
	Elapsed          types.Int64  `tfsdk:"elapsed"`
	ETA              types.Int64  `tfsdk:"eta"`
}

type RebalanceCleanupWarningsModel struct {
	Count       types.Int64  `tfsdk:"count"`
	LastMessage types.String `tfsdk:"last_message"`
	LastBucket  types.String `tfsdk:"last_bucket"`
	LastObject  types.String `tfsdk:"last_object"`
	LastAt      types.String `tfsdk:"last_at"`
	Entries     types.List   `tfsdk:"entries"`
}

type RebalanceCleanupWarningEntryModel struct {
	Bucket    types.String `tfsdk:"bucket"`
	Object    types.String `tfsdk:"object"`
	Message   types.String `tfsdk:"message"`
	Timestamp types.String `tfsdk:"timestamp"`
}

type RebalanceStopPropagationModel struct {
	LastAttemptAt             types.String `tfsdk:"last_attempt_at"`
	FailedPeers               types.List   `tfsdk:"failed_peers"`
	TerminalReloadAttemptAt   types.String `tfsdk:"terminal_reload_attempt_at"`
	TerminalReloadFailedPeers types.List   `tfsdk:"terminal_reload_failed_peers"`
	PendingTerminalReload     types.Bool   `tfsdk:"pending_terminal_reload"`
}

var rebalanceProgressAttrTypes = map[string]attr.Type{
	"objects":           types.Int64Type,
	"versions":          types.Int64Type,
	"bytes":             types.Int64Type,
	"remaining_buckets": types.Int64Type,
	"bucket":            types.StringType,
	"object":            types.StringType,
	"elapsed":           types.Int64Type,
	"eta":               types.Int64Type,
}

var rebalanceCleanupWarningEntryObjectType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"bucket":    types.StringType,
	"object":    types.StringType,
	"message":   types.StringType,
	"timestamp": types.StringType,
}}

var rebalanceCleanupWarningsAttrTypes = map[string]attr.Type{
	"count":        types.Int64Type,
	"last_message": types.StringType,
	"last_bucket":  types.StringType,
	"last_object":  types.StringType,
	"last_at":      types.StringType,
	"entries":      types.ListType{ElemType: rebalanceCleanupWarningEntryObjectType},
}

var rebalancePoolAttrTypes = map[string]attr.Type{
	"id":               types.Int64Type,
	"status":           types.StringType,
	"stopping":         types.BoolType,
	"used":             types.Float64Type,
	"last_error":       types.StringType,
	"cleanup_warnings": types.ObjectType{AttrTypes: rebalanceCleanupWarningsAttrTypes},
	"progress":         types.ObjectType{AttrTypes: rebalanceProgressAttrTypes},
}

var rebalanceStopPropagationAttrTypes = map[string]attr.Type{
	"last_attempt_at":              types.StringType,
	"failed_peers":                 types.ListType{ElemType: types.StringType},
	"terminal_reload_attempt_at":   types.StringType,
	"terminal_reload_failed_peers": types.ListType{ElemType: types.StringType},
	"pending_terminal_reload":      types.BoolType,
}

func rebalancePoolsObjectType() attr.Type {
	return types.ObjectType{AttrTypes: rebalancePoolAttrTypes}
}

func NewRebalanceResource() resource.Resource {
	return &RebalanceResource{}
}

func (r *RebalanceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_rebalance"
}

func (r *RebalanceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Trigger RustFS pool rebalancing",
		MarkdownDescription: "Triggers a pool rebalancing operation in RustFS and reports its progress.\n\n" +
			"Creating the resource starts a rebalance. Refreshing mirrors " +
			"`GET /rustfs/admin/v3/rebalance/status`: the trigger id, the overall " +
			"state and the per-pool progress, usage and cleanup warnings. A server " +
			"that does not implement the status route keeps the recorded trigger " +
			"without failing the refresh.\n\n" +
			"Destroying the resource stops an in-flight rebalance through " +
			"`POST /rustfs/admin/v3/rebalance/stop`. **Stopping does not move already " +
			"relocated data back**: pools stay partially drained, and stopping a " +
			"rebalance that already finished is a no-op. The status route is " +
			"deployment-wide, so the resource only observes and stops the run it " +
			"started (or the run recorded by the legacy fixed id); a rebalance " +
			"started outside Terraform is reported and left alone. When the server " +
			"does not implement the status or stop route, destroy warns and leaves " +
			"any running rebalance in place.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Rebalance trigger ID reported by the server.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"status": schema.StringAttribute{
				Computed: true,
				Description: "Overall rebalance state derived from the per-pool report: `Started` while a pool is " +
					"moving data or stopping, `Failed` when a pool failed, `Stopped` after a stop was requested, " +
					"`Completed` when the run finished, or `None` when no run is recorded.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"stopped_at": schema.StringAttribute{
				Computed:    true,
				Description: "Time a stop was issued, as reported by the server in RFC 3339. Null while the rebalance has not been stopped.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"pools": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Per-pool rebalance state, including inactive pools.",
				PlanModifiers: []planmodifier.List{
					listplanmodifier.UseStateForUnknown(),
				},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.Int64Attribute{
							Computed:    true,
							Description: "Zero-based pool index.",
						},
						"status": schema.StringAttribute{
							Computed:    true,
							Description: "Pool rebalance status: `None`, `Started`, `Completed`, `Stopped` or `Failed`.",
						},
						"stopping": schema.BoolAttribute{
							Computed:    true,
							Description: "Whether a stop was requested but the worker has not acknowledged it yet.",
						},
						"used": schema.Float64Attribute{
							Computed:    true,
							Description: "Fraction of the pool's space in use, in the range 0.0 to 1.0.",
						},
						"last_error": schema.StringAttribute{
							Computed:    true,
							Description: "Last rebalance error reported for the pool.",
						},
						"progress": schema.SingleNestedAttribute{
							Computed:    true,
							Description: "Progress of the pool's move. Null when the pool is not moving data.",
							Attributes: map[string]schema.Attribute{
								"objects": schema.Int64Attribute{
									Computed:    true,
									Description: "Objects moved so far.",
								},
								"versions": schema.Int64Attribute{
									Computed:    true,
									Description: "Object versions moved so far.",
								},
								"bytes": schema.Int64Attribute{
									Computed:    true,
									Description: "Bytes moved so far.",
								},
								"remaining_buckets": schema.Int64Attribute{
									Computed:    true,
									Description: "Buckets still waiting to be moved.",
								},
								"bucket": schema.StringAttribute{
									Computed:    true,
									Description: "Bucket currently being moved.",
								},
								"object": schema.StringAttribute{
									Computed:    true,
									Description: "Object currently being moved.",
								},
								"elapsed": schema.Int64Attribute{
									Computed:    true,
									Description: "Seconds elapsed since the move started.",
								},
								"eta": schema.Int64Attribute{
									Computed:    true,
									Description: "Estimated seconds remaining, when the server can compute one.",
								},
							},
						},
						"cleanup_warnings": schema.SingleNestedAttribute{
							Computed:    true,
							Description: "Source-cleanup warnings recorded while moving the pool's data.",
							Attributes: map[string]schema.Attribute{
								"count": schema.Int64Attribute{
									Computed:    true,
									Description: "Number of cleanup warnings.",
								},
								"last_message": schema.StringAttribute{
									Computed:    true,
									Description: "Message of the most recent cleanup warning.",
								},
								"last_bucket": schema.StringAttribute{
									Computed:    true,
									Description: "Bucket of the most recent cleanup warning.",
								},
								"last_object": schema.StringAttribute{
									Computed:    true,
									Description: "Object of the most recent cleanup warning.",
								},
								"last_at": schema.StringAttribute{
									Computed:    true,
									Description: "Time of the most recent cleanup warning, in RFC 3339.",
								},
								"entries": schema.ListNestedAttribute{
									Computed:    true,
									Description: "Cleanup warnings retained by the server.",
									NestedObject: schema.NestedAttributeObject{
										Attributes: map[string]schema.Attribute{
											"bucket": schema.StringAttribute{
												Computed:    true,
												Description: "Bucket the warning belongs to.",
											},
											"object": schema.StringAttribute{
												Computed:    true,
												Description: "Object the warning belongs to.",
											},
											"message": schema.StringAttribute{
												Computed:    true,
												Description: "Warning message.",
											},
											"timestamp": schema.StringAttribute{
												Computed:    true,
												Description: "Time of the warning, in RFC 3339.",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			"stop_propagation": schema.SingleNestedAttribute{
				Computed:    true,
				Description: "How the last stop request propagated across the deployment.",
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.UseStateForUnknown(),
				},
				Attributes: map[string]schema.Attribute{
					"last_attempt_at": schema.StringAttribute{
						Computed:    true,
						Description: "Time of the last stop attempt, in RFC 3339.",
					},
					"failed_peers": schema.ListAttribute{
						Computed:    true,
						ElementType: types.StringType,
						Description: "Peers that failed to acknowledge the stop.",
					},
					"terminal_reload_attempt_at": schema.StringAttribute{
						Computed:    true,
						Description: "Time the terminal rebalance state was reloaded across the deployment, in RFC 3339.",
					},
					"terminal_reload_failed_peers": schema.ListAttribute{
						Computed:    true,
						ElementType: types.StringType,
						Description: "Peers that failed to reload the terminal rebalance state.",
					},
					"pending_terminal_reload": schema.BoolAttribute{
						Computed:    true,
						Description: "Whether the terminal rebalance state still has to be reloaded across the deployment.",
					},
				},
			},
		},
	}
}

func (r *RebalanceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*AllClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *AllClient, got: %T.", req.ProviderData))
		return
	}
	r.client = client
}

func (r *RebalanceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan RebalanceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := r.client.RustClient.StartRebalance(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error starting rebalance", "Could not start rebalance: "+err.Error())
		return
	}
	if id == "" {
		resp.Diagnostics.AddError("Error starting rebalance", "The server accepted the rebalance but did not return a trigger id.")
		return
	}
	plan.ID = types.StringValue(id)

	// A report may not be visible yet, or the status route may not be
	// implemented. Observation never fails a trigger that the server accepted,
	// and a report for another run never replaces the trigger just recorded.
	if report, statusErr := r.client.RustClient.RebalanceStatus(ctx); statusErr == nil {
		if report.ID == "" || report.ID == id {
			resp.Diagnostics.Append(applyRebalanceReport(ctx, &plan, report)...)
			if resp.Diagnostics.HasError() {
				return
			}
		} else {
			tflog.Debug(ctx, "status reports a different rebalance run; keeping the trigger just started", map[string]any{"trigger": id, "reported": report.ID})
		}
	} else if isRebalanceNotStarted(statusErr) || isRebalanceUnsupported(statusErr) {
		tflog.Debug(ctx, "started a rebalance but it is not observable yet", map[string]any{"id": id, "error": statusErr.Error()})
	} else {
		resp.Diagnostics.AddWarning(
			"Rebalance started but not observable",
			"The server accepted the rebalance with trigger id "+id+", but reading its status failed: "+
				statusErr.Error()+". Refresh once the status route is reachable.",
		)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *RebalanceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state RebalanceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	report, err := r.client.RustClient.RebalanceStatus(ctx)
	if err != nil {
		// A missing record and a route the server does not implement both mean
		// there is nothing to mirror. Keep what was recorded: removing the
		// trigger would make the next plan recreate it, which is drift the
		// server did not report.
		if isRebalanceNotStarted(err) || isRebalanceUnsupported(err) {
			tflog.Debug(ctx, "rebalance status is not available; keeping the recorded trigger", map[string]any{"error": err.Error()})
			return
		}
		resp.Diagnostics.AddError("Error reading rebalance", "Could not read rebalance status: "+err.Error())
		return
	}

	if !rebalanceReportMatchesState(report, state.ID.ValueString()) {
		// The status route is deployment-wide. A report for another run says
		// nothing about the trigger this resource recorded, so keep the state
		// instead of adopting a run Terraform did not start.
		tflog.Debug(ctx, "status reports a different rebalance run; keeping the recorded trigger", map[string]any{
			"trigger":  state.ID.ValueString(),
			"reported": report.ID,
		})
		return
	}

	resp.Diagnostics.Append(applyRebalanceReport(ctx, &state, report)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *RebalanceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan RebalanceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// The resource has no configurable attributes, so Terraform never plans an
	// update. Writing the plan back keeps state consistent if it happens anyway.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *RebalanceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state RebalanceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	report, err := r.client.RustClient.RebalanceStatus(ctx)
	if err != nil {
		switch {
		case isRebalanceNotStarted(err):
			// Nothing is running: destroy is already complete.
			return
		case isRebalanceUnsupported(err):
			// The server cannot tell us whether a run is in flight and, without
			// the status route, cannot be stopped through this API either.
			resp.Diagnostics.AddWarning(
				"Rebalance status is not supported",
				"The server does not implement GET /rustfs/admin/v3/rebalance/status, so Terraform cannot tell whether a rebalance is running. Any in-flight rebalance is left alone.",
			)
			return
		default:
			resp.Diagnostics.AddError("Error stopping rebalance", "Could not read rebalance status: "+err.Error())
			return
		}
	}

	if !rebalanceReportMatchesState(report, state.ID.ValueString()) {
		// The deployment reports another run. Stopping it would cancel work this
		// resource did not start, so leave it alone and say so when it is active.
		if rebalanceIsRunning(report) {
			resp.Diagnostics.AddWarning(
				"Destroy left a different rebalance alone",
				"The deployment reports the running rebalance "+report.ID+", but this resource tracks "+
					state.ID.ValueString()+". Terraform did not start that run and does not stop it.",
			)
		}
		return
	}

	if !rebalanceIsRunning(report) {
		tflog.Trace(ctx, "rebalance is not running; nothing to stop")
		return
	}

	if err := r.client.RustClient.StopRebalance(ctx); err != nil {
		switch {
		case isRebalanceNotStarted(err):
			// The run finished between the status read and the stop.
			return
		case isRebalanceUnsupported(err):
			resp.Diagnostics.AddWarning(
				"Rebalance stop is not supported",
				"The server does not implement POST /rustfs/admin/v3/rebalance/stop, so the running rebalance keeps moving data. Stop it on the server if needed.",
			)
			return
		default:
			resp.Diagnostics.AddError("Error stopping rebalance", "Could not stop rebalance: "+err.Error())
			return
		}
	}
	tflog.Trace(ctx, "stopped a rebalance", map[string]any{"id": report.ID})
}

func (r *RebalanceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// The trigger id is the import id; Read fills the computed report.
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// rebalanceReportMatchesState reports whether the deployment's report describes
// the run this resource recorded. The status route cannot be queried by id, so
// this comparison is the only way to avoid adopting or stopping a rebalance
// started outside Terraform. The legacy fixed id is adopted once, which migrates
// state written before the server returned a trigger id.
func rebalanceReportMatchesState(report rustfs.RebalanceReport, stateID string) bool {
	return report.ID == stateID || stateID == "" || stateID == legacyRebalanceID
}

// rebalanceIsRunning reports whether any pool is still moving data. A pool that
// already has a stop in flight is not stopped a second time.
func rebalanceIsRunning(report rustfs.RebalanceReport) bool {
	for _, pool := range report.Pools {
		if pool.Status == "Started" && !pool.Stopping {
			return true
		}
	}
	return false
}

// overallRebalanceStatus collapses the per-pool statuses into one state. The
// route reports status per pool, so the resource derives a single summary:
// Started wins while any pool is active, then Failed, then Stopped, then
// Completed.
func overallRebalanceStatus(report rustfs.RebalanceReport) string {
	for _, pool := range report.Pools {
		if pool.Stopping || pool.Status == "Started" {
			return "Started"
		}
	}
	for _, pool := range report.Pools {
		if pool.Status == "Failed" {
			return "Failed"
		}
	}
	if report.StoppedAt != nil {
		return "Stopped"
	}
	for _, pool := range report.Pools {
		if pool.Status == "Stopped" {
			return "Stopped"
		}
	}
	for _, pool := range report.Pools {
		if pool.Status == "Completed" {
			return "Completed"
		}
	}
	return "None"
}

// isRebalanceNotStarted recognises the route's missing-record answer. The code
// is what separates it from a route the server does not implement, which also
// answers 404.
func isRebalanceNotStarted(err error) bool {
	return rustfs.IsAPIErrorCode(err, "NoSuchResource")
}

// isRebalanceUnsupported recognises a server that does not serve the rebalance
// status or stop routes.
//
// RustFS answers 501 NotImplemented for a route it registers without
// implementing, and a bare 404/405 for a route that was never registered. A
// structured 404/405 with another code comes from an implemented route (or from
// something in front of it), so it is a real error rather than a capability gap.
func isRebalanceUnsupported(err error) bool {
	var apiErr *rustfs.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if strings.EqualFold(apiErr.Code, "NotImplemented") {
		return true
	}
	switch apiErr.StatusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return apiErr.Code == ""
	case http.StatusNotImplemented:
		return true
	}
	return false
}

// applyRebalanceReport copies the server report into model and sets every
// computed attribute, so a plan never sees unknown leftovers.
func applyRebalanceReport(ctx context.Context, model *RebalanceResourceModel, report rustfs.RebalanceReport) diag.Diagnostics {
	var diags diag.Diagnostics

	if report.ID != "" {
		model.ID = types.StringValue(report.ID)
	}
	model.Status = types.StringValue(overallRebalanceStatus(report))
	model.StoppedAt = types.StringPointerValue(report.StoppedAt)

	pools := make([]RebalancePoolModel, 0, len(report.Pools))
	for _, pool := range report.Pools {
		progress := types.ObjectNull(rebalanceProgressAttrTypes)
		if pool.Progress != nil {
			var progressDiags diag.Diagnostics
			progress, progressDiags = types.ObjectValueFrom(ctx, rebalanceProgressAttrTypes, RebalanceProgressModel{
				Objects:          types.Int64Value(pool.Progress.Objects),
				Versions:         types.Int64Value(pool.Progress.Versions),
				Bytes:            types.Int64Value(pool.Progress.Bytes),
				RemainingBuckets: types.Int64Value(pool.Progress.RemainingBuckets),
				Bucket:           types.StringValue(pool.Progress.Bucket),
				Object:           types.StringValue(pool.Progress.Object),
				Elapsed:          types.Int64Value(pool.Progress.Elapsed),
				ETA:              types.Int64Value(pool.Progress.ETA),
			})
			diags.Append(progressDiags...)
		}

		entries := make([]RebalanceCleanupWarningEntryModel, 0, len(pool.CleanupWarnings.Entries))
		for _, entry := range pool.CleanupWarnings.Entries {
			entries = append(entries, RebalanceCleanupWarningEntryModel{
				Bucket:    types.StringValue(entry.Bucket),
				Object:    types.StringValue(entry.Object),
				Message:   types.StringValue(entry.Message),
				Timestamp: types.StringPointerValue(entry.Timestamp),
			})
		}
		entriesValue, entriesDiags := types.ListValueFrom(ctx, rebalanceCleanupWarningEntryObjectType, entries)
		diags.Append(entriesDiags...)

		warnings, warningsDiags := types.ObjectValueFrom(ctx, rebalanceCleanupWarningsAttrTypes, RebalanceCleanupWarningsModel{
			Count:       types.Int64Value(pool.CleanupWarnings.Count),
			LastMessage: types.StringPointerValue(pool.CleanupWarnings.LastMessage),
			LastBucket:  types.StringPointerValue(pool.CleanupWarnings.LastBucket),
			LastObject:  types.StringPointerValue(pool.CleanupWarnings.LastObject),
			LastAt:      types.StringPointerValue(pool.CleanupWarnings.LastAt),
			Entries:     entriesValue,
		})
		diags.Append(warningsDiags...)

		pools = append(pools, RebalancePoolModel{
			ID:              types.Int64Value(pool.ID),
			Status:          types.StringValue(pool.Status),
			Stopping:        types.BoolValue(pool.Stopping),
			Used:            types.Float64Value(pool.Used),
			LastError:       types.StringPointerValue(pool.LastError),
			CleanupWarnings: warnings,
			Progress:        progress,
		})
	}

	poolsValue, poolsDiags := types.ListValueFrom(ctx, rebalancePoolsObjectType(), pools)
	diags.Append(poolsDiags...)
	model.Pools = poolsValue

	failedPeers, failedPeersDiags := types.ListValueFrom(ctx, types.StringType, report.StopPropagation.FailedPeers)
	diags.Append(failedPeersDiags...)
	terminalReloadFailedPeers, terminalReloadFailedPeersDiags := types.ListValueFrom(ctx, types.StringType, report.StopPropagation.TerminalReloadFailedPeers)
	diags.Append(terminalReloadFailedPeersDiags...)

	stopPropagation, stopPropagationDiags := types.ObjectValueFrom(ctx, rebalanceStopPropagationAttrTypes, RebalanceStopPropagationModel{
		LastAttemptAt:             types.StringPointerValue(report.StopPropagation.LastAttemptAt),
		FailedPeers:               failedPeers,
		TerminalReloadAttemptAt:   types.StringPointerValue(report.StopPropagation.TerminalReloadAttemptAt),
		TerminalReloadFailedPeers: terminalReloadFailedPeers,
		PendingTerminalReload:     types.BoolValue(report.StopPropagation.PendingTerminalReload),
	})
	diags.Append(stopPropagationDiags...)
	model.StopPropagation = stopPropagation

	return diags
}

func init() {
	registerResource(NewRebalanceResource)
}
