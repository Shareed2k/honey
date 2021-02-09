package gcp

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/configstruct"
	"google.golang.org/api/compute/v1"

	"github.com/shareed2k/honey/pkg/place"
)

const Name = "gcp"

type (
	Backend struct {
		opt Options
	}

	// Options defines the configuration for this backend
	Options struct {
		Projects fs.CommaSepList `config:"projects"`
	}
)

// Register with Backend
func init() {
	place.Register(&place.RegInfo{
		Name:       Name,
		NewBackend: NewBackend,
		Options: []place.Option{
			{
				Name:    "projects",
				Help:    "projects list",
				Default: fs.CommaSepList{},
			},
		},
	})
}

func NewBackend(ctx context.Context, m configmap.Mapper) (place.Backend, error) {
	// Parse config into Options struct
	opt := new(Options)
	err := configstruct.Set(m, opt)
	if err != nil {
		return nil, err
	}

	if len(opt.Projects) == 0 {
		return nil, errors.New("you must specify at least one project")
	}

	return &Backend{
		opt: *opt,
	}, nil
}

func (b *Backend) Name() string {
	return Name
}

func (b *Backend) List(ctx context.Context, pattern string) (place.Printable, error) {
	computeService, err := compute.NewService(ctx)
	if err != nil {
		return nil, err
	}

	instances := make([]*place.Instance, 0)
	for _, project := range b.opt.Projects {
		call := computeService.Instances.AggregatedList(project)
		call.Filter(fmt.Sprintf("name eq .*%s.*", pattern))
		call.Pages(ctx, func(page *compute.InstanceAggregatedList) error {
			for _, items := range page.Items {
				for _, instance := range items.Instances {
					privateIP := ""
					publicIP := ""
					if len(instance.NetworkInterfaces) > 0 && instance.NetworkInterfaces[0].NetworkIP != "" {
						privateIP = instance.NetworkInterfaces[0].NetworkIP

						if len(instance.NetworkInterfaces[0].AccessConfigs) > 0 && instance.NetworkInterfaces[0].AccessConfigs[0].NatIP != "" {
							publicIP = instance.NetworkInterfaces[0].AccessConfigs[0].NatIP
						}
					}

					m := strings.Split(instance.MachineType, "/")
					instances = append(instances, &place.Instance{
						Model: place.Model{
							BackendName: Name,
							ID:          strconv.FormatUint(instance.Id, 10),
							Name:        instance.Name,
							Type:        m[len(m)-1],
							Status:      instance.Status,
							PrivateIP:   privateIP,
							PublicIP:    publicIP,
						},
						Raw: instance,
					})
				}
			}

			return nil
		})
	}

	return instances, nil
}
