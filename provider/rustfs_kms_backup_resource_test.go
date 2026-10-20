// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestKmsBackupResourceSchema(t *testing.T) {
	sch := resourceSchemaOf(t, NewKmsBackupResource())

	for _, want := range []string{"id", "backup_id", "snapshot_generation", "deployment_identity", "artifact_count", "manifest_digest", "backup_kek_id", "backup_kek_version", "sanitized_config_included", "export_supported", "unavailable_reason"} {
		if _, ok := sch.Attributes[want]; !ok {
			t.Errorf("missing attribute %q", want)
		}
	}
	backupID, ok := sch.Attributes["backup_id"].(schema.StringAttribute)
	if !ok || !backupID.Required {
		t.Fatalf("backup_id must be Required: %#v", sch.Attributes["backup_id"])
	}
	if !hasRequiresReplace(backupID.PlanModifiers) {
		t.Error("backup_id must force replacement")
	}
}

func kmsBackupLifecycleModel() *kmsBackupResourceModel {
	return &kmsBackupResourceModel{
		ID:                      types.StringValue("nightly-1"),
		BackupID:                types.StringValue("nightly-1"),
		SnapshotGeneration:      types.Int64Value(42),
		DeploymentIdentity:      types.StringValue("deploy-1"),
		ArtifactCount:           types.Int64Value(3),
		ManifestDigest:          types.StringValue("abc123"),
		BackupKEKID:             types.StringValue("kek-1"),
		BackupKEKVersion:        types.Int64Value(3),
		SanitizedConfigIncluded: types.BoolValue(true),
		ExportSupported:         types.BoolValue(true),
		UnavailableReason:       types.StringNull(),
	}
}

func kmsBackupStatusBody(exportSupported bool, reason string) string {
	return `{"service_status":"healthy","backend":"local","responsibility":"full-material","export_supported":` + boolString(exportSupported) + `,"deployment_identity":"deploy-1","backup_root":"/backups","backup_kek_configured":true,"backup_kek_id":"kek-1","backup_kek_version":3,"target_key_dir":"/keys","unavailable_reason":` + reason + `}`
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func TestKmsBackupResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	t.Run("create records the bundle metadata", func(t *testing.T) {
		client, rec := routedClient(t,
			requestRoute{method: http.MethodGet, pathContains: "kms/backup", status: http.StatusOK, body: kmsBackupStatusBody(true, "null")},
			requestRoute{method: http.MethodPost, pathContains: "kms/backup", status: http.StatusOK, body: `{"backup_id":"nightly-1","snapshot_generation":42,"deployment_identity":"deploy-1","artifact_count":3,"manifest_digest":"abc123","backup_kek_id":"kek-1","backup_kek_version":3,"sanitized_config_included":true}`},
		)
		h := newCRUDHarness(t, NewKmsBackupResource(), client)

		plan := kmsBackupLifecycleModel()
		plan.ID = types.StringNull()
		plan.SnapshotGeneration = types.Int64Null()
		plan.DeploymentIdentity = types.StringNull()
		plan.ArtifactCount = types.Int64Null()
		plan.ManifestDigest = types.StringNull()
		plan.BackupKEKID = types.StringNull()
		plan.BackupKEKVersion = types.Int64Null()
		plan.SanitizedConfigIncluded = types.BoolNull()
		plan.ExportSupported = types.BoolNull()
		plan.UnavailableReason = types.StringNull()

		resp := h.create(plan)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "kms/backup")
		assertStateString(t, resp.State, path.Root("backup_id"), "nightly-1")
		assertStateString(t, resp.State, path.Root("manifest_digest"), "abc123")
	})

	t.Run("create reports an unsupported backend", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodPost, pathContains: "kms/backup", status: http.StatusNotImplemented,
			body: `{"message":"backend aws cannot export a RustFS bundle"}`,
		})
		h := newCRUDHarness(t, NewKmsBackupResource(), client)

		resp := h.create(kmsBackupLifecycleModel())

		assertErrorDiagnostic(t, resp.Diagnostics, "Error creating KMS backup", "does not support KMS backup")
	})

	t.Run("read keeps the bundle when the subsystem becomes unavailable", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodGet, pathContains: "kms/backup", status: http.StatusOK,
			body: kmsBackupStatusBody(false, `"RUSTFS_KMS_BACKUP_DIR is not set"`),
		})
		h := newCRUDHarness(t, NewKmsBackupResource(), client)

		resp := h.read(kmsBackupLifecycleModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		assertStateString(t, resp.State, path.Root("manifest_digest"), "abc123")
		assertStateString(t, resp.State, path.Root("unavailable_reason"), "RUSTFS_KMS_BACKUP_DIR is not set")
	})

	t.Run("read reports a status failure", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method: http.MethodGet, pathContains: "kms/backup", status: http.StatusInternalServerError,
			body: s3ErrorXML("InternalError", "kms service is not running"),
		})
		h := newCRUDHarness(t, NewKmsBackupResource(), client)

		resp := h.read(kmsBackupLifecycleModel())

		assertErrorDiagnostic(t, resp.Diagnostics, "Error reading KMS backup status", "Could not read KMS backup status: ")
	})

	t.Run("delete makes no request", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewKmsBackupResource(), client)

		resp := h.delete(kmsBackupLifecycleModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "kms/backup")
	})
}
