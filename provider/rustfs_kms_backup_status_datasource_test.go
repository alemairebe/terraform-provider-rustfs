// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
)

func TestKmsBackupStatusDataSourceSchema(t *testing.T) {
	ds := NewKmsBackupStatusDataSource()
	resp := &datasource.SchemaResponse{}
	ds.Schema(context.Background(), datasource.SchemaRequest{}, resp)

	for _, want := range []string{"service_status", "backend", "responsibility", "export_supported", "deployment_identity", "backup_root", "backup_kek_configured", "backup_kek_id", "backup_kek_version", "target_key_dir", "unavailable_reason"} {
		if _, ok := resp.Schema.Attributes[want]; !ok {
			t.Errorf("missing attribute %q", want)
		}
	}
}

func TestKmsBackupStatusDataSourceMetadata(t *testing.T) {
	ds := NewKmsBackupStatusDataSource()
	resp := &datasource.MetadataResponse{}
	ds.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: providerTypeName}, resp)

	if resp.TypeName != "rustfs_kms_backup_status" {
		t.Fatalf("type name = %q, want rustfs_kms_backup_status", resp.TypeName)
	}
}

func TestKmsBackupStatusModelFromStatus(t *testing.T) {
	backend := "local"
	responsibility := "full-material"
	deployment := "deploy-1"
	root := "/backups"
	kekID := "kek-1"
	version := int64(3)
	target := "/keys"
	reason := "degraded"
	status := rustfs.KmsBackupStatus{
		ServiceStatus:       "healthy",
		Backend:             &backend,
		Responsibility:      &responsibility,
		ExportSupported:     true,
		DeploymentIdentity:  &deployment,
		BackupRoot:          &root,
		BackupKEKConfigured: true,
		BackupKEKID:         &kekID,
		BackupKEKVersion:    &version,
		TargetKeyDir:        &target,
		UnavailableReason:   &reason,
	}

	model := kmsBackupStatusModelFromStatus(status)

	if model.ServiceStatus.ValueString() != "healthy" || !model.ExportSupported.ValueBool() {
		t.Errorf("unexpected model: %+v", model)
	}
	if model.Backend.ValueString() != "local" || model.Responsibility.ValueString() != "full-material" {
		t.Errorf("unexpected identity fields: %+v", model)
	}
	if model.BackupKEKVersion.ValueInt64() != 3 || !model.BackupKEKConfigured.ValueBool() {
		t.Errorf("unexpected kek fields: %+v", model)
	}
	if model.UnavailableReason.ValueString() != "degraded" {
		t.Errorf("unexpected unavailable reason: %+v", model.UnavailableReason)
	}
}

func TestKmsBackupStatusModelFromStatusNulls(t *testing.T) {
	model := kmsBackupStatusModelFromStatus(rustfs.KmsBackupStatus{ServiceStatus: "unavailable"})

	if !model.Backend.IsNull() || !model.Responsibility.IsNull() || !model.BackupKEKVersion.IsNull() || !model.UnavailableReason.IsNull() {
		t.Errorf("expected nullable fields to stay null: %+v", model)
	}
}
