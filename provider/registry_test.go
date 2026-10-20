// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// goldenResourceTypeNames is every resource the provider must register. A
// dropped or misspelled registration fails TestRegisteredResourcesMatchGolden,
// which is the only place the type list is still written down.
var goldenResourceTypeNames = []string{
	"rustfs_audit_target",
	"rustfs_bucket",
	"rustfs_bucket_cors",
	"rustfs_bucket_durability",
	"rustfs_bucket_encryption",
	"rustfs_bucket_lifecycle_configuration",
	"rustfs_bucket_metadata_backup_import",
	"rustfs_bucket_notification",
	"rustfs_bucket_object_lock",
	"rustfs_bucket_policy",
	"rustfs_bucket_public_access_block",
	"rustfs_bucket_replication",
	"rustfs_bucket_tags",
	"rustfs_bucket_versioning",
	"rustfs_config",
	"rustfs_group",
	"rustfs_group_policy_attachment",
	"rustfs_iam_backup_import",
	"rustfs_kms_key",
	"rustfs_ldap_policy_attachment",
	"rustfs_ldap_service_account",
	"rustfs_module_switch",
	"rustfs_notification_target",
	"rustfs_policy",
	"rustfs_quota",
	"rustfs_rebalance",
	"rustfs_remote_target",
	"rustfs_serviceaccount",
	"rustfs_site_replication",
	"rustfs_table_bucket",
	"rustfs_tier",
	"rustfs_user",
	"rustfs_user_policy_attachment",
}

// goldenDataSourceTypeNames is every data source the provider must register.
var goldenDataSourceTypeNames = []string{
	"rustfs_bucket_metadata_backup",
	"rustfs_groups",
	"rustfs_health_info",
	"rustfs_iam_backup",
	"rustfs_iam_policies",
	"rustfs_iam_policy",
	"rustfs_ilm_tier_stats",
	"rustfs_kms_config",
	"rustfs_kms_status",
	"rustfs_metrics",
	"rustfs_pools",
	"rustfs_quota",
	"rustfs_replication_metrics",
	"rustfs_server_info",
	"rustfs_storage_info",
	"rustfs_user_mfa",
	"rustfs_users",
}

func TestRegisteredResourcesMatchGolden(t *testing.T) {
	got := resourceTypeNames(t, registeredResources())
	if !slices.Equal(got, goldenResourceTypeNames) {
		t.Fatalf("registered resources = %v, want %v", got, goldenResourceTypeNames)
	}
}

func TestRegisteredDataSourcesMatchGolden(t *testing.T) {
	got := dataSourceTypeNames(t, registeredDataSources())
	if !slices.Equal(got, goldenDataSourceTypeNames) {
		t.Fatalf("registered data sources = %v, want %v", got, goldenDataSourceTypeNames)
	}
}

func TestProviderResourcesUseRegistry(t *testing.T) {
	p := New("test")()

	resourceNames := resourceTypeNames(t, p.Resources(context.Background()))
	if !slices.Equal(resourceNames, goldenResourceTypeNames) {
		t.Errorf("Provider.Resources() = %v, want %v", resourceNames, goldenResourceTypeNames)
	}

	dataSourceNames := dataSourceTypeNames(t, p.DataSources(context.Background()))
	if !slices.Equal(dataSourceNames, goldenDataSourceTypeNames) {
		t.Errorf("Provider.DataSources() = %v, want %v", dataSourceNames, goldenDataSourceTypeNames)
	}
}

func TestRegistryOrderIsStable(t *testing.T) {
	first := resourceTypeNames(t, registeredResources())
	second := resourceTypeNames(t, registeredResources())
	if !slices.Equal(first, second) {
		t.Errorf("registeredResources() order changed between calls: %v then %v", first, second)
	}

	first = dataSourceTypeNames(t, registeredDataSources())
	second = dataSourceTypeNames(t, registeredDataSources())
	if !slices.Equal(first, second) {
		t.Errorf("registeredDataSources() order changed between calls: %v then %v", first, second)
	}
}

func TestRegisteredTypesAreUnique(t *testing.T) {
	assertUnique(t, "resource", resourceTypeNames(t, registeredResources()))
	assertUnique(t, "data source", dataSourceTypeNames(t, registeredDataSources()))
}

func resourceTypeNames(t *testing.T, factories []func() resource.Resource) []string {
	t.Helper()

	names := make([]string, 0, len(factories))
	for _, factory := range factories {
		var resp resource.MetadataResponse
		factory().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: providerTypeName}, &resp)
		names = append(names, resp.TypeName)
	}
	return names
}

func dataSourceTypeNames(t *testing.T, factories []func() datasource.DataSource) []string {
	t.Helper()

	names := make([]string, 0, len(factories))
	for _, factory := range factories {
		var resp datasource.MetadataResponse
		factory().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: providerTypeName}, &resp)
		names = append(names, resp.TypeName)
	}
	return names
}

func assertUnique(t *testing.T, kind string, names []string) {
	t.Helper()

	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, duplicate := seen[name]; duplicate {
			t.Errorf("%s %q is registered more than once", kind, name)
		}
		seen[name] = struct{}{}
	}
}
