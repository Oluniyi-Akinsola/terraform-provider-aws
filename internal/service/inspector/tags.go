//go:build !generate
// +build !generate

package inspector

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/inspector"
	tftags "github.com/hashicorp/terraform-provider-aws/internal/tags"
)

// Custom Inspector tag service update functions using the same format as generated code.

// updateTags updates WorkSpaces resource tags.
// The identifier is the resource ARN.
func updateTags(ctx context.Context, conn *inspector.Inspector, identifier string, oldTagsMap interface{}, newTagsMap interface{}) error {
	oldTags := tftags.New(ctx, oldTagsMap)
	newTags := tftags.New(ctx, newTagsMap)

	if len(newTags) > 0 {
		input := &inspector.SetTagsForResourceInput{
			ResourceArn: aws.String(identifier),
			Tags:        Tags(newTags.IgnoreAWS()),
		}

		_, err := conn.SetTagsForResourceWithContext(ctx, input)

		if err != nil {
			return fmt.Errorf("error tagging resource (%s): %w", identifier, err)
		}
	} else if len(oldTags) > 0 {
		input := &inspector.SetTagsForResourceInput{
			ResourceArn: aws.String(identifier),
		}

		_, err := conn.SetTagsForResourceWithContext(ctx, input)

		if err != nil {
			return fmt.Errorf("error untagging resource (%s): %w", identifier, err)
		}
	}

	return nil
}
