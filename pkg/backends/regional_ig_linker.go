/*
Copyright 2021 The Kubernetes Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package backends

import (
	"fmt"

	cloudprovider "github.com/GoogleCloudPlatform/k8s-cloud-provider/pkg/cloud"
	"github.com/GoogleCloudPlatform/k8s-cloud-provider/pkg/cloud/meta"
	"k8s.io/ingress-gce/pkg/composite"
	"k8s.io/ingress-gce/pkg/instancegroups"
	"k8s.io/ingress-gce/pkg/utils"
	"k8s.io/klog/v2"
)

// RegionalInstanceGroupLinker handles linking backends to InstanceGroups.
type RegionalInstanceGroupLinker struct {
	instancePool instancegroups.Manager
	backendPool  Pool
}

func NewRegionalInstanceGroupLinker(instancePool instancegroups.Manager, backendPool Pool) *RegionalInstanceGroupLinker {
	return &RegionalInstanceGroupLinker{
		instancePool: instancePool,
		backendPool:  backendPool,
	}
}

// Link performs linking instance groups to regional backend service
func (linker *RegionalInstanceGroupLinker) Link(sp utils.ServicePort, projectID string, zones []string) error {
	klog.V(2).Infof("Link(%v, %q, %v)", sp, projectID, zones)

	var igLinks []string
	for _, zone := range zones {
		key := meta.ZonalKey(sp.IGName(), zone)
		igSelfLink := cloudprovider.SelfLink(meta.VersionGA, projectID, "instanceGroups", key)
		igLinks = append(igLinks, igSelfLink)
	}
	// TODO(cheungdavid): Create regional ig linker logger that contains backendName,
	// backendVersion, and backendScope before passing to backendPool.Get().
	// See example in backendSyncer.ensureBackendService().
	bs, err := linker.backendPool.Get(sp.BackendName(), meta.VersionGA, meta.Regional, klog.TODO())
	if err != nil {
		return err
	}
	addIGs, removeIGs, err := getInstanceGroupsToAddAndRemove(bs, igLinks)
	if err != nil {
		return err
	}
	if len(addIGs) == 0 && len(removeIGs) == 0 {
		klog.V(3).Infof("No backends to add or remove for %s, skipping update.", sp.BackendName())
		return nil
	}

	if len(removeIGs) != 0 {
		var backendsWithoutRemoved []*composite.Backend
		for _, b := range bs.Backends {
			path, err := utils.RelativeResourceName(b.Group)
			if err != nil {
				return err
			}
			if !removeIGs.Has(path) {
				backendsWithoutRemoved = append(backendsWithoutRemoved, b)
			}
		}
		bs.Backends = backendsWithoutRemoved
	}

	for _, igLink := range addIGs {
		b := &composite.Backend{
			Group: igLink,
		}
		bs.Backends = append(bs.Backends, b)
	}

	klog.V(3).Infof("Update Backend %s, with %d added backends (total %d).", sp.BackendName(), len(addIGs), len(bs.Backends))
	if err := linker.backendPool.Update(bs, klog.TODO()); err != nil {
		return fmt.Errorf("updating backend service %s for IG failed, err:%w", sp.BackendName(), err)
	}
	return nil
}
