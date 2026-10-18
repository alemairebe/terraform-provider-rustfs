package provider

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccPolicyResource_basic(t *testing.T) {
	name := fmt.Sprintf("tf-test-policy-%d", acctest.RandInt())
	resourceName := "rustfs_policy.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckPolicyDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccPolicyConfig(name),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPolicyExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "name", name),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        name,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
			},
		},
	})
}

func testAccPolicyConfig(name string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_policy" "test" {
  name = "%s"
  statement = [{
    effect    = "Allow"
    action    = ["s3:GetObject", "s3:ListBucket"]
    resource = ["arn:aws:s3:::*"]
  }]
}
`, name)
}

func testAccCheckPolicyExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}
		policyName := rs.Primary.Attributes["name"]
		if policyName == "" {
			return fmt.Errorf("no policy name set")
		}
		client := testAccPolicyClient()
		_, err := client.ReadPolicy(policyName)
		if err != nil {
			return fmt.Errorf("policy not found: %s", err)
		}
		return nil
	}
}

func testAccCheckPolicyDestroy(s *terraform.State) error {
	client := testAccPolicyClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_policy" {
			continue
		}
		policyName := rs.Primary.Attributes["name"]
		if policyName == "" {
			continue
		}
		_, err := client.ReadPolicy(policyName)
		if err == nil {
			return fmt.Errorf("policy %s still exists", policyName)
		}
	}
	return nil
}

func testAccPolicyClient() rustfs.RustfsAdmin {
	return rustfs.New(&rustfs.RustfsAdminConfig{
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
	})
}

func TestPolicyResourceSchema(t *testing.T) {
	r := NewPolicyResource()
	resp := &frameworkresource.SchemaResponse{}
	r.Schema(context.TODO(), frameworkresource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{"name", "version", "statement"} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("expected %q attribute", name)
		}
	}

	statement, ok := attrs["statement"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatalf("statement attribute is not a ListNestedAttribute")
	}

	nested := statement.NestedObject.Attributes
	for _, name := range []string{"effect", "sid", "action", "not_action", "resource", "not_resource", "condition"} {
		if _, ok := nested[name]; !ok {
			t.Errorf("expected nested %q attribute", name)
		}
	}

	action, ok := nested["action"].(schema.SetAttribute)
	if !ok {
		t.Fatalf("action attribute is not a SetAttribute")
	}
	if !action.Optional || action.Required {
		t.Errorf("action must be optional, got optional=%t required=%t", action.Optional, action.Required)
	}

	notAction, ok := nested["not_action"].(schema.SetAttribute)
	if !ok {
		t.Fatalf("not_action attribute is not a SetAttribute")
	}
	if !notAction.Optional || notAction.Required {
		t.Errorf("not_action must be optional, got optional=%t required=%t", notAction.Optional, notAction.Required)
	}

	condition, ok := nested["condition"].(schema.MapAttribute)
	if !ok {
		t.Fatalf("condition attribute is not a MapAttribute")
	}
	wantConditionType := types.MapType{ElemType: types.ListType{ElemType: types.StringType}}
	if !condition.ElementType.Equal(wantConditionType) {
		t.Errorf("condition element type is %s, want %s", condition.ElementType, wantConditionType)
	}

	if len(statement.NestedObject.Validators) != 1 {
		t.Errorf("expected one statement validator, got %d", len(statement.NestedObject.Validators))
	}
}

func TestPolicyResourceMetadata(t *testing.T) {
	r := NewPolicyResource()
	resp := &frameworkresource.MetadataResponse{}
	r.Metadata(context.TODO(), frameworkresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_policy" {
		t.Errorf("expected rustfs_policy, got %s", resp.TypeName)
	}
}

func TestPolicyStatementValidator(t *testing.T) {
	statementType := map[string]attr.Type{
		"action":     types.SetType{ElemType: types.StringType},
		"not_action": types.SetType{ElemType: types.StringType},
	}
	statement := func(action, notAction attr.Value) types.Object {
		return types.ObjectValueMust(statementType, map[string]attr.Value{
			"action":     action,
			"not_action": notAction,
		})
	}

	tests := map[string]struct {
		object  types.Object
		wantErr bool
	}{
		"action only": {
			object: statement(types.SetValueMust(types.StringType, []attr.Value{types.StringValue("s3:GetObject")}), types.SetNull(types.StringType)),
		},
		"not_action only": {
			object: statement(types.SetNull(types.StringType), types.SetValueMust(types.StringType, []attr.Value{types.StringValue("sts:AssumeRole")})),
		},
		"both": {
			object: statement(types.SetValueMust(types.StringType, []attr.Value{types.StringValue("s3:GetObject")}), types.SetValueMust(types.StringType, []attr.Value{types.StringValue("sts:AssumeRole")})),
		},
		"neither": {
			object:  statement(types.SetNull(types.StringType), types.SetNull(types.StringType)),
			wantErr: true,
		},
		"unknown action defers": {
			object: statement(types.SetUnknown(types.StringType), types.SetNull(types.StringType)),
		},
		"unknown not_action defers": {
			object: statement(types.SetNull(types.StringType), types.SetUnknown(types.StringType)),
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			resp := &validator.ObjectResponse{}
			policyStatementValidator{}.ValidateObject(context.TODO(), validator.ObjectRequest{
				ConfigValue: testCase.object,
				Path:        path.Root("statement").AtListIndex(0),
			}, resp)

			if got := resp.Diagnostics.HasError(); got != testCase.wantErr {
				t.Fatalf("error = %t, want %t: %v", got, testCase.wantErr, resp.Diagnostics)
			}
			if !testCase.wantErr {
				return
			}

			withPath, ok := resp.Diagnostics[0].(diag.DiagnosticWithPath)
			if !ok {
				t.Fatalf("diagnostic %T does not carry a path", resp.Diagnostics[0])
			}
			wantPath := path.Root("statement").AtListIndex(0).AtName("not_action")
			if !withPath.Path().Equal(wantPath) {
				t.Errorf("diagnostic path is %s, want %s", withPath.Path(), wantPath)
			}
		})
	}
}

func TestPolicyStatementsRoundTrip(t *testing.T) {
	models := []policyStatementModel{
		{
			Effect:   "Allow",
			Sid:      types.StringValue("PrefixAccess"),
			Action:   []string{"s3:ListBucket"},
			Resource: []string{"arn:aws:s3:::app-data"},
			Condition: map[string]map[string][]string{
				"StringLike": {"s3:prefix": {"tenant-42/*"}},
			},
		},
		{
			Effect:      "Deny",
			Sid:         types.StringNull(),
			NotAction:   []string{"sts:AssumeRole"},
			NotResource: []string{"arn:aws:s3:::other"},
		},
	}

	statements := policyStatementsFromModel(models)
	if len(statements) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(statements))
	}
	if statements[0].Sid != "PrefixAccess" {
		t.Errorf("expected Sid PrefixAccess, got %q", statements[0].Sid)
	}
	if statements[1].Sid != "" {
		t.Errorf("expected empty Sid, got %q", statements[1].Sid)
	}
	if statements[1].Action != nil || statements[1].Resource != nil {
		t.Errorf("expected no Action/Resource, got %v / %v", statements[1].Action, statements[1].Resource)
	}
	wantCondition := rustfs.PolicyCondition{"StringLike": {"s3:prefix": {"tenant-42/*"}}}
	if !reflect.DeepEqual(statements[0].Condition, wantCondition) {
		t.Errorf("wrong condition: %v", statements[0].Condition)
	}

	back := policyStatementModelsFromAPI(statements)
	if !reflect.DeepEqual(back, models) {
		t.Errorf("round trip mismatch:\n got %#v\nwant %#v", back, models)
	}
}

func testAccPolicyConditionConfig(name string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_policy" "test" {
  name = "%s"
  statement = [
    {
      sid      = "PrefixAccess"
      effect   = "Allow"
      action   = ["s3:ListBucket"]
      resource = ["arn:aws:s3:::app-data"]

      condition = {
        StringLike = {
          "s3:prefix" = ["tenant-42/*"]
        }
      }
    },
    {
      effect       = "Allow"
      not_action   = ["sts:AssumeRole"]
      not_resource = ["arn:aws:s3:::other"]
    },
  ]
}
`, name)
}

func testAccPolicyMissingActionConfig(name string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_policy" "test" {
  name = "%s"
  statement = [{
    effect   = "Allow"
    resource = ["arn:aws:s3:::app-data"]
  }]
}
`, name)
}

func TestAccPolicyResource_condition(t *testing.T) {
	name := fmt.Sprintf("tf-test-policy-cond-%d", acctest.RandInt())
	resourceName := "rustfs_policy.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckPolicyDestroy,
		Steps: []resource.TestStep{
			{
				// The validation error must fire before anything is created, and
				// the step has to run first: the testing framework destroys the
				// last configured resource, which a rejected config cannot do.
				Config:      testAccPolicyMissingActionConfig(fmt.Sprintf("tf-test-policy-invalid-%d", acctest.RandInt())),
				ExpectError: regexp.MustCompile("must set either action or not_action"),
			},
			{
				Config: testAccPolicyConditionConfig(name),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckPolicyExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "name", name),
					resource.TestCheckResourceAttr(resourceName, "statement.0.sid", "PrefixAccess"),
					resource.TestCheckResourceAttr(resourceName, "statement.0.action.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "statement.0.action.0", "s3:ListBucket"),
					resource.TestCheckResourceAttr(resourceName, "statement.0.condition.%", "1"),
					resource.TestCheckResourceAttr(resourceName, "statement.0.condition.StringLike.%", "1"),
					resource.TestCheckResourceAttr(resourceName, "statement.0.condition.StringLike.s3:prefix.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "statement.0.condition.StringLike.s3:prefix.0", "tenant-42/*"),
					resource.TestCheckResourceAttr(resourceName, "statement.1.not_action.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "statement.1.not_action.0", "sts:AssumeRole"),
					resource.TestCheckResourceAttr(resourceName, "statement.1.not_resource.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "statement.1.not_resource.0", "arn:aws:s3:::other"),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        name,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "name",
			},
		},
	})
}
