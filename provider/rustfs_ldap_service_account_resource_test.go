// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestAccLdapServiceAccount creates a service account scoped to an LDAP user.
// The endpoint only accepts target users that already exist in the identity
// store, which requires an LDAP identity provider on the RustFS cluster.
//
// No supported RustFS version can be given one: `help-config-kv` lists
// identity_openid but no identity_ldap sub-system, the server binary carries no
// RUSTFS_*LDAP* environment variable, and add-user rejects the distinguished
// names these endpoints expect (verified against 1.0.1 through latest). The
// test therefore skips on every version and the create/read/update/delete/
// import flows are covered by TestLDAPServiceAccountResource* against a mocked
// admin API. Set RUSTFS_LDAP_TEST_USER_DN (and RUSTFS_ENDPOINT/RUSTFS_USER/
// RUSTFS_SECRET) to run it against a cluster that does have an LDAP IdP.
func TestAccLdapServiceAccount(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC must be set for acceptance tests")
	}
	accessKey := fmt.Sprintf("tf-ldap-sa-%d", acctest.RandInt())
	resourceName := "rustfs_ldap_service_account.test"
	dn := envOrDefault("RUSTFS_LDAP_TEST_USER_DN", "uid=alice,ou=people,dc=example,dc=com")

	client := rustfs.New(&rustfs.RustfsAdminConfig{
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
	})

	// Probe the endpoint: if the LDAP user is not provisioned, the server
	// rejects the create with "target user not exist". Skip in that case so
	// the acceptance run stays green on clusters without an LDAP IdP.
	probeKey := accessKey + "-probe"
	err := client.CreateLDAPServiceAccount(rustfs.ServiceAccount{
		AccessKey:  probeKey,
		SecretKey:  "probeSecret",
		Name:       "ldap-probe",
		TargetUser: dn,
	})
	if err != nil {
		t.Skipf("skipping: no LDAP IdP configured or LDAP user %q missing on the cluster: %v "+
			"(RustFS 1.0.x cannot be given an LDAP identity provider; the CRUD flows are covered "+
			"by TestLDAPServiceAccountResource* against a mocked admin API)", dn, err)
	}
	if delErr := client.DeleteServiceAccount(rustfs.ServiceAccount{AccessKey: probeKey}); delErr != nil {
		t.Fatalf("could not clean up probe service account: %v", delErr)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckLdapServiceAccountDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccLdapServiceAccountConfig(accessKey, dn, "ldap-token"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "access_key", accessKey),
					resource.TestCheckResourceAttr(resourceName, "user", dn),
					resource.TestCheckResourceAttr(resourceName, "name", "ldap-token"),
				),
			},
			{
				Config: testAccLdapServiceAccountConfig(accessKey, dn, "renamed-token"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "name", "renamed-token"),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        accessKey,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "access_key",
				ImportStateVerifyIgnore:              []string{"secret_key"},
			},
		},
	})
}

func testAccLdapServiceAccountConfig(accessKey, user, name string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_ldap_service_account" "test" {
  access_key = "%s"
  secret_key = "superSecret123!"
  name       = "%s"
  user       = %q
}
`, accessKey, name, user)
}

func testAccCheckLdapServiceAccountDestroy(s *terraform.State) error {
	client := rustfs.New(&rustfs.RustfsAdminConfig{
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
	})
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_ldap_service_account" {
			continue
		}
		accessKey := rs.Primary.Attributes["access_key"]
		if accessKey == "" {
			continue
		}
		_, err := client.ReadServiceAccount(accessKey)
		if err == nil {
			return fmt.Errorf("service account %s still exists", accessKey)
		}
	}
	return nil
}

// The acceptance test above skips itself: RustFS 1.0.x cannot be given an LDAP
// identity provider, so `idp/ldap/add-service-account` answers "target user not
// exist" on every supported version and no fixture can change that (see
// .github/CONTRIBUTING.md, "Acceptance tests"). The flows below drive Create,
// Read, Update, Delete and ImportState against the mocked admin API harness in
// provider_test.go instead, which also pins the wire contract: an LDAP service
// account is an ordinary service account whose `targetUser` carries the
// distinguished name.

const (
	ldapSAAddPath    = "/rustfs/admin/v3/idp/ldap/add-service-account"
	ldapSAReadPath   = "/rustfs/admin/v3/info-service-account"
	ldapSAUpdatePath = "/rustfs/admin/v3/update-service-account"
	ldapSADeletePath = "/rustfs/admin/v3/delete-service-accounts"
	ldapSAUserDN     = "uid=alice,ou=people,dc=example,dc=com"
)

// ldapSAAccessKey and ldapSASecret are the plan's credentials: the mocked admin
// API never authenticates and failure messages never echo the values.
var (
	ldapSAAccessKey = unitTestCredential("RUSTFS_TEST_LDAP_SA_ACCESS_KEY", 'a')
	ldapSASecret    = unitTestCredential("RUSTFS_TEST_LDAP_SA_SECRET_KEY", 's')
)

func ldapSAPlan(accessKey, name string) LDAPServiceAccountResourceModel {
	return LDAPServiceAccountResourceModel{
		AccessKey:   types.StringValue(accessKey),
		SecretKey:   types.StringValue(ldapSASecret),
		Name:        types.StringValue(name),
		Description: types.StringNull(),
		User:        types.StringValue(ldapSAUserDN),
		Policy:      types.StringNull(),
	}
}

// ldapSAReadReply is the mocked info-service-account payload.
var ldapSAReadReply = `{"accessKey":"` + ldapSAAccessKey + `","name":"ldap-token",` +
	`"description":"read back from the server","parentUser":"` + ldapSAUserDN + `",` +
	`"impliedPolicy":false,"policy":"{\"Version\":\"2012-10-17\"}"}`

// assertLDAPSAModelEqual reports the fields that differ, without printing the
// secret key.
func assertLDAPSAModelEqual(t *testing.T, what string, got, want LDAPServiceAccountResourceModel) {
	t.Helper()
	if got.AccessKey != want.AccessKey {
		t.Errorf("%s: access_key differs from the plan (value redacted)", what)
	}
	if got.Name != want.Name {
		t.Errorf("%s: name = %q, want %q", what, got.Name.ValueString(), want.Name.ValueString())
	}
	if got.Description != want.Description {
		t.Errorf("%s: description = %v, want %v", what, got.Description, want.Description)
	}
	if got.User != want.User {
		t.Errorf("%s: user = %q, want %q", what, got.User.ValueString(), want.User.ValueString())
	}
	if got.Policy != want.Policy {
		t.Errorf("%s: policy = %v, want %v", what, got.Policy, want.Policy)
	}
	if got.SecretKey.ValueString() != want.SecretKey.ValueString() {
		t.Errorf("%s: secret key differs from the plan (value redacted)", what)
	}
}

func TestLDAPServiceAccountResourceCreate(t *testing.T) {
	ctx := context.Background()
	r := &LDAPServiceAccountResource{}
	sch := resourceSchemaOf(t, r)
	srv, recorder := newRecorderServer(t, http.StatusOK, ldapSAReadReply)
	r.client = newUnitAllClient(srv)

	plan := ldapSAPlan(ldapSAAccessKey, "ldap-token")
	resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, LDAPServiceAccountResourceModel{})}
	r.Create(ctx, frameworkresource.CreateRequest{Plan: resourcePlan(t, sch, plan)}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	var created LDAPServiceAccountResourceModel
	requireNoDiagnostics(t, resp.State.Get(ctx, &created))
	assertLDAPSAModelEqual(t, "create state", created, plan)

	request := recorder.only(t)
	if request.Method != http.MethodPut || request.Path != ldapSAAddPath {
		t.Fatalf("create sent %s %s, want PUT %s", request.Method, request.Path, ldapSAAddPath)
	}
	var sent rustfs.ServiceAccount
	if err := json.Unmarshal(request.Body, &sent); err != nil {
		t.Fatalf("create body is not a service account: %v", err)
	}
	if sent.TargetUser != ldapSAUserDN {
		t.Errorf("create body targetUser = %q, want the LDAP DN %q", sent.TargetUser, ldapSAUserDN)
	}
	if sent.AccessKey != ldapSAAccessKey || sent.Name != "ldap-token" {
		t.Error("create body must carry the planned access key and name (values redacted)")
	}
	if sent.SecretKey != ldapSASecret {
		t.Error("create body did not carry the planned secret key (value redacted)")
	}
	// normalizeServiceAccount defaults the expiry and marks an account without a
	// policy document as implied-policy.
	if sent.Expiration != "9999-01-01T00:00:00Z" || !sent.ImpliedPolicy || sent.Policy != "" {
		t.Errorf("create body defaults: expiration=%q impliedPolicy=%v policy=%q",
			sent.Expiration, sent.ImpliedPolicy, sent.Policy)
	}
}

func TestLDAPServiceAccountResourceCreateError(t *testing.T) {
	r := &LDAPServiceAccountResource{}
	sch := resourceSchemaOf(t, r)
	srv, _ := newRecorderServer(t, http.StatusInternalServerError, internalErrorBody)
	r.client = newUnitAllClient(srv)

	resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, LDAPServiceAccountResourceModel{})}
	r.Create(context.Background(), frameworkresource.CreateRequest{Plan: resourcePlan(t, sch, ldapSAPlan(ldapSAAccessKey, "ldap-token"))}, resp)
	assertDiagnosticSummary(t, resp.Diagnostics, "Error creating LDAP service account")
}

func TestLDAPServiceAccountResourceRead(t *testing.T) {
	ctx := context.Background()
	r := &LDAPServiceAccountResource{}
	sch := resourceSchemaOf(t, r)
	srv, recorder := newRecorderServer(t, http.StatusOK, ldapSAReadReply)
	r.client = newUnitAllClient(srv)

	state := ldapSAPlan(ldapSAAccessKey, "ldap-token")
	resp := &frameworkresource.ReadResponse{State: resourceState(t, sch, state)}
	r.Read(ctx, frameworkresource.ReadRequest{State: resourceState(t, sch, state)}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	var read LDAPServiceAccountResourceModel
	requireNoDiagnostics(t, resp.State.Get(ctx, &read))
	if read.Name.ValueString() != "ldap-token" ||
		read.Description.ValueString() != "read back from the server" ||
		read.User.ValueString() != ldapSAUserDN {
		t.Errorf("read state name/description/user = %q/%q/%q",
			read.Name.ValueString(), read.Description.ValueString(), read.User.ValueString())
	}
	// The state had no policy of its own, so the server's document is adopted.
	if read.Policy.ValueString() != `{"Version":"2012-10-17"}` {
		t.Errorf("read policy = %q, want the server's document", read.Policy.ValueString())
	}
	if got := queryValue(t, recorder.only(t), "accessKey"); got != ldapSAAccessKey {
		t.Error("read must request the state's access key (value redacted)")
	}
}

func TestLDAPServiceAccountResourceReadError(t *testing.T) {
	r := &LDAPServiceAccountResource{}
	sch := resourceSchemaOf(t, r)
	srv, _ := newRecorderServer(t, http.StatusInternalServerError, internalErrorBody)
	r.client = newUnitAllClient(srv)

	state := ldapSAPlan(ldapSAAccessKey, "ldap-token")
	resp := &frameworkresource.ReadResponse{State: resourceState(t, sch, state)}
	r.Read(context.Background(), frameworkresource.ReadRequest{State: resourceState(t, sch, state)}, resp)
	assertDiagnosticSummary(t, resp.Diagnostics, "Error reading LDAP service account")
}

func TestLDAPServiceAccountResourceReadKeepsConfiguredPolicy(t *testing.T) {
	ctx := context.Background()
	r := &LDAPServiceAccountResource{}
	sch := resourceSchemaOf(t, r)
	// The server reports an implied (parent-derived) policy and no description
	// or parent user; a policy the configuration set must survive the read.
	srv, _ := newRecorderServer(t, http.StatusOK,
		`{"accessKey":"`+ldapSAAccessKey+`","name":"ldap-token","impliedPolicy":true}`)
	r.client = newUnitAllClient(srv)

	state := ldapSAPlan(ldapSAAccessKey, "ldap-token")
	state.Policy = types.StringValue(`{"Version":"2012-10-17","Statement":[]}`)
	state.Description = types.StringValue("kept description")
	state.User = types.StringValue(ldapSAUserDN)

	resp := &frameworkresource.ReadResponse{State: resourceState(t, sch, state)}
	r.Read(ctx, frameworkresource.ReadRequest{State: resourceState(t, sch, state)}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	var read LDAPServiceAccountResourceModel
	requireNoDiagnostics(t, resp.State.Get(ctx, &read))
	if read.Policy.ValueString() != `{"Version":"2012-10-17","Statement":[]}` {
		t.Errorf("read policy = %q, want the configured document", read.Policy.ValueString())
	}
	if read.Description.ValueString() != "kept description" || read.User.ValueString() != ldapSAUserDN {
		t.Errorf("read dropped configured values: description=%q user=%q",
			read.Description.ValueString(), read.User.ValueString())
	}
	if read.Name.ValueString() != "ldap-token" {
		t.Errorf("read name = %q", read.Name.ValueString())
	}
}

func TestLDAPServiceAccountResourceUpdate(t *testing.T) {
	ctx := context.Background()
	r := &LDAPServiceAccountResource{}
	sch := resourceSchemaOf(t, r)
	srv, recorder := newRecorderServer(t, http.StatusOK, "")
	r.client = newUnitAllClient(srv)

	state := ldapSAPlan(ldapSAAccessKey, "ldap-token")
	plan := ldapSAPlan(ldapSAAccessKey, "renamed-token")
	resp := &frameworkresource.UpdateResponse{State: resourceState(t, sch, state)}
	r.Update(ctx, frameworkresource.UpdateRequest{
		Plan:  resourcePlan(t, sch, plan),
		State: resourceState(t, sch, state),
	}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	var afterUpdate LDAPServiceAccountResourceModel
	requireNoDiagnostics(t, resp.State.Get(ctx, &afterUpdate))
	assertLDAPSAModelEqual(t, "update state", afterUpdate, plan)

	request := recorder.only(t)
	if request.Method != http.MethodPost || request.Path != ldapSAUpdatePath {
		t.Fatalf("update sent %s %s, want POST %s", request.Method, request.Path, ldapSAUpdatePath)
	}
	if got := queryValue(t, request, "accessKey"); got != ldapSAAccessKey {
		t.Error("update must target the state's access key (value redacted)")
	}
	var updateBody map[string]any
	if err := json.Unmarshal(request.Body, &updateBody); err != nil {
		t.Fatalf("update body is not JSON: %v", err)
	}
	if updateBody["newName"] != "renamed-token" || updateBody["newAccessKey"] != plan.AccessKey.ValueString() {
		t.Error("update body newName/newAccessKey do not match the plan (values redacted)")
	}
	if _, ok := updateBody["newSecretKey"]; !ok {
		t.Error("update body must carry the rotated secret key")
	}
}

func TestLDAPServiceAccountResourceUpdateError(t *testing.T) {
	r := &LDAPServiceAccountResource{}
	sch := resourceSchemaOf(t, r)
	srv, _ := newRecorderServer(t, http.StatusInternalServerError, internalErrorBody)
	r.client = newUnitAllClient(srv)

	state := ldapSAPlan(ldapSAAccessKey, "ldap-token")
	resp := &frameworkresource.UpdateResponse{State: resourceState(t, sch, state)}
	r.Update(context.Background(), frameworkresource.UpdateRequest{
		Plan:  resourcePlan(t, sch, state),
		State: resourceState(t, sch, state),
	}, resp)
	assertDiagnosticSummary(t, resp.Diagnostics, "Error updating LDAP service account")
}

func TestLDAPServiceAccountResourceDelete(t *testing.T) {
	ctx := context.Background()
	r := &LDAPServiceAccountResource{}
	sch := resourceSchemaOf(t, r)
	srv, recorder := newRecorderServer(t, http.StatusOK, "")
	r.client = newUnitAllClient(srv)

	state := ldapSAPlan(ldapSAAccessKey, "ldap-token")
	resp := &frameworkresource.DeleteResponse{State: resourceState(t, sch, state)}
	r.Delete(ctx, frameworkresource.DeleteRequest{State: resourceState(t, sch, state)}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	request := recorder.only(t)
	if request.Method != http.MethodDelete || request.Path != ldapSADeletePath {
		t.Fatalf("delete sent %s %s, want DELETE %s", request.Method, request.Path, ldapSADeletePath)
	}
	if got := queryValue(t, request, "accessKey"); got != ldapSAAccessKey {
		t.Error("delete must target the state's access key (value redacted)")
	}
	if len(request.Body) != 0 {
		t.Errorf("delete sent a body of %d bytes, want none", len(request.Body))
	}
}

func TestLDAPServiceAccountResourceDeleteError(t *testing.T) {
	r := &LDAPServiceAccountResource{}
	sch := resourceSchemaOf(t, r)
	srv, _ := newRecorderServer(t, http.StatusInternalServerError, internalErrorBody)
	r.client = newUnitAllClient(srv)

	state := ldapSAPlan(ldapSAAccessKey, "ldap-token")
	resp := &frameworkresource.DeleteResponse{State: resourceState(t, sch, state)}
	r.Delete(context.Background(), frameworkresource.DeleteRequest{State: resourceState(t, sch, state)}, resp)
	assertDiagnosticSummary(t, resp.Diagnostics, "Error deleting LDAP service account")
}

func TestLDAPServiceAccountResourceImportState(t *testing.T) {
	ctx := context.Background()
	r := &LDAPServiceAccountResource{}
	sch := resourceSchemaOf(t, r)

	resp := &frameworkresource.ImportStateResponse{State: resourceState(t, sch, LDAPServiceAccountResourceModel{})}
	r.ImportState(ctx, frameworkresource.ImportStateRequest{ID: ldapSAAccessKey}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	var accessKey types.String
	requireNoDiagnostics(t, resp.State.GetAttribute(ctx, path.Root("access_key"), &accessKey))
	if accessKey.ValueString() != ldapSAAccessKey {
		t.Error("imported access_key does not match the import ID (value redacted)")
	}
}
