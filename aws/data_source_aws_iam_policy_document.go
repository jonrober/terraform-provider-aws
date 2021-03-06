package aws

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform/helper/hashcode"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/hashicorp/terraform/helper/validation"
)

var dataSourceAwsIamPolicyDocumentVarReplacer = strings.NewReplacer("&{", "${")

func dataSourceAwsIamPolicyDocument() *schema.Resource {
	setOfString := &schema.Schema{
		Type:     schema.TypeSet,
		Optional: true,
		Elem: &schema.Schema{
			Type: schema.TypeString,
		},
	}

	return &schema.Resource{
		Read: dataSourceAwsIamPolicyDocumentRead,

		Schema: map[string]*schema.Schema{
			"override_json": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"policy_id": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"source_json": {
				Type:     schema.TypeString,
				Optional: true,
			},
			"statement": {
				Type:     schema.TypeList,
				Optional: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"sid": {
							Type:     schema.TypeString,
							Optional: true,
						},
						"effect": {
							Type:         schema.TypeString,
							Optional:     true,
							Default:      "Allow",
							ValidateFunc: validation.StringInSlice([]string{"Allow", "Deny"}, false),
						},
						"actions":        setOfString,
						"not_actions":    setOfString,
						"resources":      setOfString,
						"not_resources":  setOfString,
						"principals":     dataSourceAwsIamPolicyPrincipalSchema(),
						"not_principals": dataSourceAwsIamPolicyPrincipalSchema(),
						"condition": {
							Type:     schema.TypeSet,
							Optional: true,
							Elem: &schema.Resource{
								Schema: map[string]*schema.Schema{
									"test": {
										Type:     schema.TypeString,
										Required: true,
									},
									"variable": {
										Type:     schema.TypeString,
										Required: true,
									},
									"values": {
										Type:     schema.TypeSet,
										Required: true,
										Elem: &schema.Schema{
											Type: schema.TypeString,
										},
									},
								},
							},
						},
					},
				},
			},
			"json": {
				Type:     schema.TypeString,
				Computed: true,
			},
		},
	}
}

func dataSourceAwsIamPolicyDocumentRead(d *schema.ResourceData, meta interface{}) error {
	mergedDoc := &IAMPolicyDoc{}

	// populate mergedDoc directly with any source_json
	if sourceJSON, hasSourceJSON := d.GetOk("source_json"); hasSourceJSON {
		if err := json.Unmarshal([]byte(sourceJSON.(string)), mergedDoc); err != nil {
			return err
		}
	}

	// process the current document
	doc := &IAMPolicyDoc{}

	doc.Version = "2012-10-17"

	if policyID, hasPolicyID := d.GetOk("policy_id"); hasPolicyID {
		doc.Id = policyID.(string)
	}

	if cfgStmts, hasCfgStmts := d.GetOk("statement"); hasCfgStmts {
		var cfgStmtIntf = cfgStmts.([]interface{})
		stmts := make([]*IAMPolicyStatement, len(cfgStmtIntf))
		sidMap := make(map[string]struct{})

		for i, stmtI := range cfgStmtIntf {
			cfgStmt := stmtI.(map[string]interface{})
			stmt := &IAMPolicyStatement{
				Effect: cfgStmt["effect"].(string),
			}

			if sid, ok := cfgStmt["sid"]; ok {
				if _, ok := sidMap[sid.(string)]; ok {
					return fmt.Errorf("Found duplicate sid (%s). Either remove the sid or ensure the sid is unique across all statements.", sid.(string))
				}
				stmt.Sid = sid.(string)
				if len(stmt.Sid) > 0 {
					sidMap[stmt.Sid] = struct{}{}
				}
			}

			if actions := cfgStmt["actions"].(*schema.Set).List(); len(actions) > 0 {
				stmt.Actions = iamPolicyDecodeConfigStringList(actions)
			}
			if actions := cfgStmt["not_actions"].(*schema.Set).List(); len(actions) > 0 {
				stmt.NotActions = iamPolicyDecodeConfigStringList(actions)
			}

			if resources := cfgStmt["resources"].(*schema.Set).List(); len(resources) > 0 {
				stmt.Resources = dataSourceAwsIamPolicyDocumentReplaceVarsInList(
					iamPolicyDecodeConfigStringList(resources),
				)
			}
			if resources := cfgStmt["not_resources"].(*schema.Set).List(); len(resources) > 0 {
				stmt.NotResources = dataSourceAwsIamPolicyDocumentReplaceVarsInList(
					iamPolicyDecodeConfigStringList(resources),
				)
			}

			if principals := cfgStmt["principals"].(*schema.Set).List(); len(principals) > 0 {
				stmt.Principals = dataSourceAwsIamPolicyDocumentMakePrincipals(principals)
			}

			if principals := cfgStmt["not_principals"].(*schema.Set).List(); len(principals) > 0 {
				stmt.NotPrincipals = dataSourceAwsIamPolicyDocumentMakePrincipals(principals)
			}

			if conditions := cfgStmt["condition"].(*schema.Set).List(); len(conditions) > 0 {
				stmt.Conditions = dataSourceAwsIamPolicyDocumentMakeConditions(conditions)
			}

			stmts[i] = stmt
		}

		doc.Statements = stmts

	}

	// merge our current document into mergedDoc
	mergedDoc.Merge(doc)

	// merge in override_json
	if overrideJSON, hasOverrideJSON := d.GetOk("override_json"); hasOverrideJSON {
		overrideDoc := &IAMPolicyDoc{}
		if err := json.Unmarshal([]byte(overrideJSON.(string)), overrideDoc); err != nil {
			return err
		}

		mergedDoc.Merge(overrideDoc)
	}

	jsonDoc, err := json.MarshalIndent(mergedDoc, "", "  ")
	if err != nil {
		// should never happen if the above code is correct
		return err
	}
	jsonString := string(jsonDoc)

	d.Set("json", jsonString)
	d.SetId(strconv.Itoa(hashcode.String(jsonString)))

	return nil
}

func dataSourceAwsIamPolicyDocumentReplaceVarsInList(in interface{}) interface{} {
	switch v := in.(type) {
	case string:
		return dataSourceAwsIamPolicyDocumentVarReplacer.Replace(v)
	case []string:
		out := make([]string, len(v))
		for i, item := range v {
			out[i] = dataSourceAwsIamPolicyDocumentVarReplacer.Replace(item)
		}
		return out
	default:
		panic("dataSourceAwsIamPolicyDocumentReplaceVarsInList: input not string nor []string")
	}
}

func dataSourceAwsIamPolicyDocumentMakeConditions(in []interface{}) IAMPolicyStatementConditionSet {
	out := make([]IAMPolicyStatementCondition, len(in))
	for i, itemI := range in {
		item := itemI.(map[string]interface{})
		out[i] = IAMPolicyStatementCondition{
			Test:     item["test"].(string),
			Variable: item["variable"].(string),
			Values: dataSourceAwsIamPolicyDocumentReplaceVarsInList(
				iamPolicyDecodeConfigStringList(
					item["values"].(*schema.Set).List(),
				),
			),
		}
	}
	return IAMPolicyStatementConditionSet(out)
}

func dataSourceAwsIamPolicyDocumentMakePrincipals(in []interface{}) IAMPolicyStatementPrincipalSet {
	out := make([]IAMPolicyStatementPrincipal, len(in))
	for i, itemI := range in {
		item := itemI.(map[string]interface{})
		out[i] = IAMPolicyStatementPrincipal{
			Type: item["type"].(string),
			Identifiers: dataSourceAwsIamPolicyDocumentReplaceVarsInList(
				iamPolicyDecodeConfigStringList(
					item["identifiers"].(*schema.Set).List(),
				),
			),
		}
	}
	return IAMPolicyStatementPrincipalSet(out)
}

func dataSourceAwsIamPolicyPrincipalSchema() *schema.Schema {
	return &schema.Schema{
		Type:     schema.TypeSet,
		Optional: true,
		Elem: &schema.Resource{
			Schema: map[string]*schema.Schema{
				"type": {
					Type:     schema.TypeString,
					Required: true,
				},
				"identifiers": {
					Type:     schema.TypeSet,
					Required: true,
					Elem: &schema.Schema{
						Type: schema.TypeString,
					},
				},
			},
		},
	}
}
