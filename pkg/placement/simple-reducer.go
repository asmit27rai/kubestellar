/*
Copyright 2023 The KCP Authors.

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

package placement

import (
	"sync"

	apimachtypes "k8s.io/apimachinery/pkg/types"

	edgeapi "github.com/kcp-dev/edge-mc/pkg/apis/edge/v1alpha1"
)

// SimplePlacementSliceSetReducer is the simplest possible
// implementation of SinglePlacementSliceSetReducer.
type SimplePlacementSliceSetReducer struct {
	sync.Mutex
	consumers []SinglePlacementSetChangeConsumer
	enhanced  SinglePlacementSet
}

var _ SinglePlacementSliceSetReducer = &SimplePlacementSliceSetReducer{}

func NewSimplePlacementSliceSetReducer(consumers ...SinglePlacementSetChangeConsumer) *SimplePlacementSliceSetReducer {
	ans := &SimplePlacementSliceSetReducer{
		consumers: consumers,
		enhanced:  NewSinglePlacementSet(),
	}
	return ans
}

// SimplePlacementSliceSetReducerAsUIDConsumer adds the Set method that
// SimplePlacementSliceSetReducer needs in order to implement
// MappingReceiver to receive UID information.
type SimplePlacementSliceSetReducerAsUIDConsumer struct {
	*SimplePlacementSliceSetReducer
}

func (spsr SimplePlacementSliceSetReducerAsUIDConsumer) Set(en ExternalName, uid apimachtypes.UID) {
	spsr.Lock()
	defer spsr.Unlock()
	if details, ok := spsr.enhanced[en]; ok {
		details.SyncTargetUID = uid
		spsr.enhanced[en] = details
		fullSP := details.Complete(en)
		for _, consumer := range spsr.consumers {
			consumer.Add(fullSP)
		}

	}
}

func (spsr *SimplePlacementSliceSetReducer) Set(newSlices ResolvedWhere) {
	spsr.Lock()
	defer spsr.Unlock()
	for key, val := range spsr.enhanced {
		sp := val.Complete(key)
		for _, consumer := range spsr.consumers {
			consumer.Remove(sp)
		}
	}
	spsr.setLocked(newSlices)
}

func (spsr *SimplePlacementSliceSetReducer) setLocked(newSlices ResolvedWhere) {
	spsr.enhanced = NewSinglePlacementSet()
	enumerateSinglePlacementSlices(newSlices, func(apiSP edgeapi.SinglePlacement) {
		syncTargetID := ExternalName{}.OfSPTarget(apiSP)
		syncTargetDetails := SinglePlacementDetails{
			LocationName:  apiSP.LocationName,
			SyncTargetUID: apiSP.SyncTargetUID,
		}
		spsr.enhanced[syncTargetID] = syncTargetDetails
		for _, consumer := range spsr.consumers {
			consumer.Add(apiSP)
		}
	})
}

func enumerateSinglePlacementSlices(slices []*edgeapi.SinglePlacementSlice, consumer func(edgeapi.SinglePlacement)) {
	for _, slice := range slices {
		if slice != nil {
			for _, sp := range slice.Destinations {
				consumer(sp)
			}
		}
	}
}
