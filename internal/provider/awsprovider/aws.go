package awsprovider

import (
	"context"
	"strings"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/hosts"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// AWS implements EC2 instance search.
type AWS struct {
	Name    string // optional config label (--backends)
	Profile string
	Region  string
}

// ID returns the honey backend identifier ("aws").
func (AWS) ID() string { return "aws" }

// BackendName returns the optional YAML backends.aws[].name value.
func (a *AWS) BackendName() string { return strings.TrimSpace(a.Name) }

// CacheIdentity scopes cache entries per profile/region pair.
func (a *AWS) CacheIdentity() string {
	return strings.TrimSpace(a.Name) + "\x1e" + a.Profile + "\x1e" + a.Region
}

var _ hosts.Backend = (*AWS)(nil)

// Search returns EC2 instances matching the query (running or pending).
func (a *AWS) Search(ctx context.Context, q hosts.Query) ([]hosts.Record, error) {
	opts := []func(*config.LoadOptions) error{}
	profile := a.Profile
	if q.AWSProfile != "" {
		profile = q.AWSProfile
	}
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	region := a.Region
	if q.AWSRegion != "" {
		region = q.AWSRegion
	}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	
	zap.L().Debug("aws starting DescribeInstances", zap.String("profile", profile), zap.String("region", cfg.Region))
	svc := ec2.NewFromConfig(cfg)

	var out []hosts.Record
	paginator := ec2.NewDescribeInstancesPaginator(svc, &ec2.DescribeInstancesInput{
		Filters: []types.Filter{
			{Name: aws.String("instance-state-name"), Values: []string{"running", "pending"}},
		},
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, rsv := range page.Reservations {
			for _, inst := range rsv.Instances {
				h, ok, err := instanceToRecord(inst, q)
				if err != nil {
					return nil, err
				}
				if ok {
					out = append(out, h)
				}
			}
		}
	}
	return out, nil
}

func instanceToRecord(inst types.Instance, q hosts.Query) (hosts.Record, bool, error) {
	name := ""
	for _, t := range inst.Tags {
		if aws.ToString(t.Key) == "Name" {
			name = aws.ToString(t.Value)
			break
		}
	}
	if name == "" && inst.InstanceId != nil {
		name = aws.ToString(inst.InstanceId)
	}
	ok, err := hosts.NameMatches(name, q)
	if err != nil {
		return hosts.Record{}, false, err
	}
	if !ok {
		return hosts.Record{}, false, nil
	}

	var ips []string
	var primary string
	if inst.PublicIpAddress != nil && aws.ToString(inst.PublicIpAddress) != "" {
		primary = aws.ToString(inst.PublicIpAddress)
		ips = append(ips, primary)
	}
	if inst.PrivateIpAddress != nil && aws.ToString(inst.PrivateIpAddress) != "" {
		pip := aws.ToString(inst.PrivateIpAddress)
		ips = append(ips, pip)
		if primary == "" {
			primary = pip
		}
	}
	if primary == "" {
		return hosts.Record{}, false, nil
	}

	az := ""
	if inst.Placement != nil && inst.Placement.AvailabilityZone != nil {
		az = aws.ToString(inst.Placement.AvailabilityZone)
	}
	region := azToRegion(az)

	meta := map[string]string{
		"instance_id": aws.ToString(inst.InstanceId),
		"state":       string(inst.State.Name),
	}

	return hosts.Record{
		Provider:  "aws",
		Name:      name,
		PrimaryIP: primary,
		ExtraIPs:  ips,
		Zone:      az,
		Region:    region,
		Meta:      meta,
	}, true, nil
}

func azToRegion(az string) string {
	if az == "" {
		return ""
	}
	// Remove trailing single letter (a-z) after last hyphen for standard zones.
	i := strings.LastIndex(az, "-")
	if i <= 0 {
		return az
	}
	suffix := az[i+1:]
	if len(suffix) == 1 && suffix[0] >= 'a' && suffix[0] <= 'z' {
		return az[:i]
	}
	return az
}
