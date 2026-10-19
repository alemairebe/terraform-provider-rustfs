// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"rustfs": providerserver.NewProtocol6WithError(New("test")()),
}

var requiredEnvVars = []string{
	"RUSTFS_ENDPOINT",
	"RUSTFS_USER",
	"RUSTFS_SECRET",
}

func testAccPreCheck(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC must be set for acceptance tests")
	}
	for _, env := range requiredEnvVars {
		if os.Getenv(env) == "" {
			t.Fatalf("%s must be set for acceptance tests", env)
		}
	}
}

func testAccProviderConfig() string {
	return `provider "rustfs" {
  endpoint   = "` + os.Getenv("RUSTFS_ENDPOINT") + `"
  access_key = "` + os.Getenv("RUSTFS_USER") + `"
  secret_key = "` + os.Getenv("RUSTFS_SECRET") + `"
  ssl        = false
}
`
}

// newNullImportState builds the all-null state the framework hands to
// ImportState, from the resource's own schema. It lets the composite import-ID
// parsing be tested without a server, which is the only way to reach it for
// resources whose acceptance tests need an identity provider.
func newNullImportState(t *testing.T, r fwresource.Resource) tfsdk.State {
	t.Helper()
	ctx := context.Background()

	schemaResp := &fwresource.SchemaResponse{}
	r.Schema(ctx, fwresource.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %s", schemaResp.Diagnostics)
	}

	schemaType := schemaResp.Schema.Type().TerraformType(ctx)
	objectType, ok := schemaType.(tftypes.Object)
	if !ok {
		t.Fatalf("expected an object schema type, got %T", schemaType)
	}
	nulls := make(map[string]tftypes.Value, len(objectType.AttributeTypes))
	for name, attrType := range objectType.AttributeTypes {
		nulls[name] = tftypes.NewValue(attrType, nil)
	}

	return tfsdk.State{
		Schema: schemaResp.Schema,
		Raw:    tftypes.NewValue(objectType, nulls),
	}
}
