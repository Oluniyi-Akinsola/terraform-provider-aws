package waf

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/arn"
	"github.com/aws/aws-sdk-go/service/waf"
	"github.com/hashicorp/aws-sdk-go-base/v2/awsv1shim/v2/tfawserr"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/hashicorp/terraform-provider-aws/internal/conns"
	"github.com/hashicorp/terraform-provider-aws/internal/errs/sdkdiag"
	tftags "github.com/hashicorp/terraform-provider-aws/internal/tags"
	"github.com/hashicorp/terraform-provider-aws/internal/tfresource"
	"github.com/hashicorp/terraform-provider-aws/internal/verify"
)

const (
	RuleDeleteTimeout = 5 * time.Minute
)

func ResourceRule() *schema.Resource {
	return &schema.Resource{
		CreateWithoutTimeout: resourceRuleCreate,
		ReadWithoutTimeout:   resourceRuleRead,
		UpdateWithoutTimeout: resourceRuleUpdate,
		DeleteWithoutTimeout: resourceRuleDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},

		Schema: map[string]*schema.Schema{
			"name": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"metric_name": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringMatch(regexp.MustCompile(`^[0-9A-Za-z]+$`), "must contain only alphanumeric characters"),
			},
			"predicates": {
				Type:     schema.TypeSet,
				Optional: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"negated": {
							Type:     schema.TypeBool,
							Required: true,
						},
						"data_id": {
							Type:         schema.TypeString,
							Required:     true,
							ValidateFunc: validation.StringLenBetween(0, 128),
						},
						"type": {
							Type:         schema.TypeString,
							Required:     true,
							ValidateFunc: validation.StringInSlice(waf.PredicateType_Values(), false),
						},
					},
				},
			},
			"tags":     tftags.TagsSchema(),
			"tags_all": tftags.TagsSchemaComputed(),
			"arn": {
				Type:     schema.TypeString,
				Computed: true,
			},
		},

		CustomizeDiff: verify.SetTagsDiff,
	}
}

func resourceRuleCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	conn := meta.(*conns.AWSClient).WAFConn()
	defaultTagsConfig := meta.(*conns.AWSClient).DefaultTagsConfig
	tags := defaultTagsConfig.MergeTags(tftags.New(ctx, d.Get("tags").(map[string]interface{})))

	wr := NewRetryer(conn)
	out, err := wr.RetryWithToken(ctx, func(token *string) (interface{}, error) {
		params := &waf.CreateRuleInput{
			ChangeToken: token,
			MetricName:  aws.String(d.Get("metric_name").(string)),
			Name:        aws.String(d.Get("name").(string)),
		}

		if len(tags) > 0 {
			params.Tags = Tags(tags.IgnoreAWS())
		}

		return conn.CreateRuleWithContext(ctx, params)
	})

	if err != nil {
		return sdkdiag.AppendErrorf(diags, "creating WAF Rule (%s): %s", d.Get("name").(string), err)
	}

	resp := out.(*waf.CreateRuleOutput)
	d.SetId(aws.StringValue(resp.Rule.RuleId))

	newPredicates := d.Get("predicates").(*schema.Set).List()
	if len(newPredicates) > 0 {
		noPredicates := []interface{}{}
		err := updateRuleResource(ctx, d.Id(), noPredicates, newPredicates, conn)
		if err != nil {
			return sdkdiag.AppendErrorf(diags, "updating WAF Rule (%s): %s", d.Id(), err)
		}
	}

	return append(diags, resourceRuleRead(ctx, d, meta)...)
}

func resourceRuleRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	conn := meta.(*conns.AWSClient).WAFConn()
	defaultTagsConfig := meta.(*conns.AWSClient).DefaultTagsConfig
	ignoreTagsConfig := meta.(*conns.AWSClient).IgnoreTagsConfig

	params := &waf.GetRuleInput{
		RuleId: aws.String(d.Id()),
	}

	resp, err := conn.GetRuleWithContext(ctx, params)
	if !d.IsNewResource() && tfawserr.ErrCodeEquals(err, waf.ErrCodeNonexistentItemException) {
		log.Printf("[WARN] WAF Rule (%s) not found, removing from state", d.Id())
		d.SetId("")
		return diags
	}

	if err != nil {
		return sdkdiag.AppendErrorf(diags, "reading WAF Rule (%s): %s", d.Id(), err)
	}

	if resp == nil || resp.Rule == nil {
		if d.IsNewResource() {
			return sdkdiag.AppendErrorf(diags, "reading WAF Rule (%s): not found", d.Id())
		}

		log.Printf("[WARN] WAF Rule (%s) not found, removing from state", d.Id())
		d.SetId("")
		return diags
	}

	var predicates []map[string]interface{}

	for _, predicateSet := range resp.Rule.Predicates {
		predicate := map[string]interface{}{
			"negated": *predicateSet.Negated,
			"type":    *predicateSet.Type,
			"data_id": *predicateSet.DataId,
		}
		predicates = append(predicates, predicate)
	}

	arn := arn.ARN{
		Partition: meta.(*conns.AWSClient).Partition,
		Service:   "waf",
		AccountID: meta.(*conns.AWSClient).AccountID,
		Resource:  fmt.Sprintf("rule/%s", d.Id()),
	}.String()
	d.Set("arn", arn)

	tags, err := ListTags(ctx, conn, arn)

	if err != nil {
		return sdkdiag.AppendErrorf(diags, "listing tags for WAF Rule (%s): %s", arn, err)
	}

	tags = tags.IgnoreAWS().IgnoreConfig(ignoreTagsConfig)

	//lintignore:AWSR002
	if err := d.Set("tags", tags.RemoveDefaultConfig(defaultTagsConfig).Map()); err != nil {
		return sdkdiag.AppendErrorf(diags, "setting tags: %s", err)
	}

	if err := d.Set("tags_all", tags.Map()); err != nil {
		return sdkdiag.AppendErrorf(diags, "setting tags_all: %s", err)
	}

	d.Set("predicates", predicates)
	d.Set("name", resp.Rule.Name)
	d.Set("metric_name", resp.Rule.MetricName)

	return diags
}

func resourceRuleUpdate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	conn := meta.(*conns.AWSClient).WAFConn()

	if d.HasChange("predicates") {
		o, n := d.GetChange("predicates")
		oldP, newP := o.(*schema.Set).List(), n.(*schema.Set).List()

		err := updateRuleResource(ctx, d.Id(), oldP, newP, conn)
		if err != nil {
			return sdkdiag.AppendErrorf(diags, "updating WAF Rule (%s): %s", d.Id(), err)
		}
	}

	if d.HasChange("tags_all") {
		o, n := d.GetChange("tags_all")

		if err := UpdateTags(ctx, conn, d.Get("arn").(string), o, n); err != nil {
			return sdkdiag.AppendErrorf(diags, "updating WAF Rule (%s) tags: %s", d.Id(), err)
		}
	}

	return append(diags, resourceRuleRead(ctx, d, meta)...)
}

func resourceRuleDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	conn := meta.(*conns.AWSClient).WAFConn()

	oldPredicates := d.Get("predicates").(*schema.Set).List()
	if len(oldPredicates) > 0 {
		noPredicates := []interface{}{}
		err := updateRuleResource(ctx, d.Id(), oldPredicates, noPredicates, conn)
		if err != nil {
			return sdkdiag.AppendErrorf(diags, "updating WAF Rule (%s) predicates: %s", d.Id(), err)
		}
	}

	wr := NewRetryer(conn)
	err := resource.RetryContext(ctx, RuleDeleteTimeout, func() *resource.RetryError {
		_, err := wr.RetryWithToken(ctx, func(token *string) (interface{}, error) {
			req := &waf.DeleteRuleInput{
				ChangeToken: token,
				RuleId:      aws.String(d.Id()),
			}

			return conn.DeleteRuleWithContext(ctx, req)
		})

		if err != nil {
			if tfawserr.ErrCodeEquals(err, waf.ErrCodeReferencedItemException) {
				return resource.RetryableError(err)
			}
			return resource.NonRetryableError(err)
		}

		return nil
	})

	if tfresource.TimedOut(err) {
		_, err = wr.RetryWithToken(ctx, func(token *string) (interface{}, error) {
			req := &waf.DeleteRuleInput{
				ChangeToken: token,
				RuleId:      aws.String(d.Id()),
			}

			return conn.DeleteRuleWithContext(ctx, req)
		})
	}

	if err != nil {
		if tfawserr.ErrCodeEquals(err, waf.ErrCodeNonexistentItemException) {
			return diags
		}
		return sdkdiag.AppendErrorf(diags, "deleting WAF Rule (%s): %s", d.Id(), err)
	}

	return diags
}

func updateRuleResource(ctx context.Context, id string, oldP, newP []interface{}, conn *waf.WAF) error {
	wr := NewRetryer(conn)
	_, err := wr.RetryWithToken(ctx, func(token *string) (interface{}, error) {
		req := &waf.UpdateRuleInput{
			ChangeToken: token,
			RuleId:      aws.String(id),
			Updates:     DiffRulePredicates(oldP, newP),
		}

		return conn.UpdateRuleWithContext(ctx, req)
	})

	return err
}
