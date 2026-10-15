package source

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// ec2Instance is the subset of DescribeInstances we care about, keyed by the
// Slurm node name it represents ("head", "compute01", "aws-burst-0").
type ec2Instance struct {
	ID         string
	State      string
	LaunchTime time.Time
	PublicIP   string
	Generation string
}

// EC2 polls instances belonging to one BurstLab cluster.
//
// Tagging in the burstlab Terraform: the head node and compute nodes carry
// Name=<cluster>-head-node / <cluster>-computeNN (their Generation tag is
// hard-coded to gen1, so we don't trust it). Burst nodes carry
// Cluster=<cluster>, Role=burst-node, and resume.py sets Name to the Slurm
// node name.
type EC2 struct {
	client  *ec2.Client
	Cluster string
	Region  string
}

func NewEC2(ctx context.Context, cluster, region, profile string) (*EC2, error) {
	opts := []func(*config.LoadOptions) error{}
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &EC2{client: ec2.NewFromConfig(cfg), Cluster: cluster, Region: cfg.Region}, nil
}

func (e *EC2) Poll(ctx context.Context) (map[string]ec2Instance, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	in := &ec2.DescribeInstancesInput{Filters: []types.Filter{
		{Name: aws.String("tag:Project"), Values: []string{"burstlab"}},
	}}
	out := map[string]ec2Instance{}
	p := ec2.NewDescribeInstancesPaginator(e.client, in)
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range page.Reservations {
			for _, i := range r.Instances {
				name, ok := e.slurmName(i)
				if !ok {
					continue
				}
				inst := ec2Instance{
					ID:         aws.ToString(i.InstanceId),
					State:      string(i.State.Name),
					LaunchTime: aws.ToTime(i.LaunchTime),
					PublicIP:   aws.ToString(i.PublicIpAddress),
					Generation: tag(i, "Generation"),
				}
				// Several instances can share a name over a demo (each burst
				// cycle launches a fresh one). Prefer live over dead, then newest.
				if prev, seen := out[name]; !seen || better(inst, prev) {
					out[name] = inst
				}
			}
		}
	}
	return out, nil
}

func better(a, b ec2Instance) bool {
	aDead, bDead := a.State == "terminated", b.State == "terminated"
	if aDead != bDead {
		return !aDead
	}
	return a.LaunchTime.After(b.LaunchTime)
}

func (e *EC2) slurmName(i types.Instance) (string, bool) {
	name := tag(i, "Name")
	switch {
	case name == e.Cluster+"-head-node":
		return "head", true
	case strings.HasPrefix(name, e.Cluster+"-compute"):
		return strings.TrimPrefix(name, e.Cluster+"-"), true
	case tag(i, "Cluster") == e.Cluster && tag(i, "Role") == "burst-node" && name != "":
		return name, true
	}
	return "", false
}

func tag(i types.Instance, key string) string {
	for _, t := range i.Tags {
		if aws.ToString(t.Key) == key {
			return aws.ToString(t.Value)
		}
	}
	return ""
}

// HeadPublicIP finds the head node's public address so the user doesn't have
// to copy it out of `terraform output`.
func (e *EC2) HeadPublicIP(ctx context.Context) (string, error) {
	m, err := e.Poll(ctx)
	if err != nil {
		return "", err
	}
	h, ok := m["head"]
	if !ok || h.PublicIP == "" || h.State != "running" {
		return "", errNoHead
	}
	return h.PublicIP, nil
}

type headErr struct{}

func (headErr) Error() string {
	return "no running head node found (is the cluster deployed? check --cluster / --region)"
}

var errNoHead = headErr{}
