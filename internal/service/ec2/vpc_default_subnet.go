package ec2

import (
	"context"
	"log"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/hashicorp/terraform-provider-aws/internal/conns"
	"github.com/hashicorp/terraform-provider-aws/internal/errs/sdkdiag"
	tftags "github.com/hashicorp/terraform-provider-aws/internal/tags"
	"github.com/hashicorp/terraform-provider-aws/internal/tfresource"
	"github.com/hashicorp/terraform-provider-aws/internal/verify"
)

func ResourceDefaultSubnet() *schema.Resource {
	//lintignore:R011
	return &schema.Resource{
		CreateWithoutTimeout: resourceDefaultSubnetCreate,
		ReadWithoutTimeout:   resourceSubnetRead,
		UpdateWithoutTimeout: resourceSubnetUpdate,
		DeleteWithoutTimeout: resourceDefaultSubnetDelete,

		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},

		CustomizeDiff: verify.SetTagsDiff,

		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(10 * time.Minute),
			Delete: schema.DefaultTimeout(20 * time.Minute),
		},

		SchemaVersion: 1,
		MigrateState:  SubnetMigrateState,

		// Keep in sync with aws_subnet's schema with the following changes:
		//   - availability_zone is Required/ForceNew
		//   - availability_zone_id is Computed-only
		//   - cidr_block is Computed-only
		//   - ipv6_cidr_block is Optional/Computed as it's automatically assigned if ipv6_native = true
		//   - map_public_ip_on_launch has a Default of true
		//   - outpost_arn is Computed-only
		//   - vpc_id is Computed-only
		// and additions:
		//   - existing_default_subnet Computed-only, set in resourceDefaultSubnetCreate
		//   - force_destroy Optional
		Schema: map[string]*schema.Schema{
			"arn": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"assign_ipv6_address_on_creation": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"availability_zone": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"availability_zone_id": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"cidr_block": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"customer_owned_ipv4_pool": {
				Type:         schema.TypeString,
				Optional:     true,
				RequiredWith: []string{"map_customer_owned_ip_on_launch"},
			},
			"enable_dns64": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"enable_resource_name_dns_aaaa_record_on_launch": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"enable_resource_name_dns_a_record_on_launch": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"existing_default_subnet": {
				Type:     schema.TypeBool,
				Computed: true,
			},
			"force_destroy": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"ipv6_cidr_block": {
				Type:         schema.TypeString,
				Optional:     true,
				Computed:     true,
				ValidateFunc: verify.ValidIPv6CIDRNetworkAddress,
			},
			"ipv6_cidr_block_association_id": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"ipv6_native": {
				Type:     schema.TypeBool,
				Optional: true,
				ForceNew: true,
				Default:  false,
			},
			"map_customer_owned_ip_on_launch": {
				Type:         schema.TypeBool,
				Optional:     true,
				RequiredWith: []string{"customer_owned_ipv4_pool", "outpost_arn"},
			},
			"map_public_ip_on_launch": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  true,
			},
			"outpost_arn": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"owner_id": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"private_dns_hostname_type_on_launch": {
				Type:         schema.TypeString,
				Optional:     true,
				Computed:     true,
				ValidateFunc: validation.StringInSlice(ec2.HostnameType_Values(), false),
			},
			"tags":     tftags.TagsSchema(),
			"tags_all": tftags.TagsSchemaComputed(),
			"vpc_id": {
				Type:     schema.TypeString,
				Computed: true,
			},
		},
	}
}

func resourceDefaultSubnetCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	conn := meta.(*conns.AWSClient).EC2Conn()

	availabilityZone := d.Get("availability_zone").(string)
	input := &ec2.DescribeSubnetsInput{
		Filters: BuildAttributeFilterList(
			map[string]string{
				"availabilityZone": availabilityZone,
				"defaultForAz":     "true",
			},
		),
	}

	var computedIPv6CIDRBlock bool
	subnet, err := FindSubnet(ctx, conn, input)

	if err == nil {
		log.Printf("[INFO] Found existing EC2 Default Subnet (%s)", availabilityZone)
		d.SetId(aws.StringValue(subnet.SubnetId))
		d.Set("existing_default_subnet", true)
	} else if tfresource.NotFound(err) {
		input := &ec2.CreateDefaultSubnetInput{
			AvailabilityZone: aws.String(availabilityZone),
		}

		var ipv6Native bool
		if v, ok := d.GetOk("ipv6_native"); ok {
			ipv6Native = v.(bool)
			input.Ipv6Native = aws.Bool(ipv6Native)
		}

		log.Printf("[DEBUG] Creating EC2 Default Subnet: %s", input)
		output, err := conn.CreateDefaultSubnetWithContext(ctx, input)

		if err != nil {
			return sdkdiag.AppendErrorf(diags, "creating EC2 Default Subnet (%s): %s", availabilityZone, err)
		}

		subnet = output.Subnet

		d.SetId(aws.StringValue(subnet.SubnetId))
		d.Set("existing_default_subnet", false)

		subnet, err = WaitSubnetAvailable(ctx, conn, d.Id(), d.Timeout(schema.TimeoutCreate))

		if err != nil {
			return sdkdiag.AppendErrorf(diags, "waiting for EC2 Default Subnet (%s) create: %s", d.Id(), err)
		}

		// Creating an IPv6-native default subnets associates an IPv6 CIDR block.
		for i, v := range subnet.Ipv6CidrBlockAssociationSet {
			if aws.StringValue(v.Ipv6CidrBlockState.State) == ec2.SubnetCidrBlockStateCodeAssociating { //we can only ever have 1 IPv6 block associated at once
				associationID := aws.StringValue(v.AssociationId)

				subnetCidrBlockState, err := WaitSubnetIPv6CIDRBlockAssociationCreated(ctx, conn, associationID)

				if err != nil {
					return sdkdiag.AppendErrorf(diags, "waiting for EC2 Default Subnet (%s) IPv6 CIDR block (%s) to become associated: %s", d.Id(), associationID, err)
				}

				subnet.Ipv6CidrBlockAssociationSet[i].Ipv6CidrBlockState = subnetCidrBlockState
			}
		}

		if ipv6Native {
			computedIPv6CIDRBlock = true
		}
	} else {
		return sdkdiag.AppendErrorf(diags, "reading EC2 Default Subnet (%s): %s", availabilityZone, err)
	}

	if err := modifySubnetAttributesOnCreate(ctx, conn, d, subnet, computedIPv6CIDRBlock); err != nil {
		return sdkdiag.AppendFromErr(diags, err)
	}

	// Configure tags.
	defaultTagsConfig := meta.(*conns.AWSClient).DefaultTagsConfig
	ignoreTagsConfig := meta.(*conns.AWSClient).IgnoreTagsConfig
	newTags := defaultTagsConfig.MergeTags(tftags.New(ctx, d.Get("tags").(map[string]interface{}))).IgnoreConfig(ignoreTagsConfig)
	oldTags := KeyValueTags(ctx, subnet.Tags).IgnoreAWS().IgnoreConfig(ignoreTagsConfig)

	if !oldTags.Equal(newTags) {
		if err := UpdateTags(ctx, conn, d.Id(), oldTags, newTags); err != nil {
			return sdkdiag.AppendErrorf(diags, "updating EC2 Default Subnet (%s) tags: %s", d.Id(), err)
		}
	}

	return append(diags, resourceSubnetRead(ctx, d, meta)...)
}

func resourceDefaultSubnetDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	if d.Get("force_destroy").(bool) {
		return append(diags, resourceSubnetDelete(ctx, d, meta)...)
	}

	log.Printf("[WARN] EC2 Default Subnet (%s) not deleted, removing from state", d.Id())

	return diags
}
