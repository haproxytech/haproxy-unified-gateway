// Copyright 2025 HAProxy Technologies LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package tree

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/google/go-cmp/cmp"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/protocols"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "sigs.k8s.io/gateway-api/apis/v1"
)

type VirtualListener struct {
	ProtocolCategory protocols.ProtocolCategory
	Name             string
	// Status
	Status    store.Status
	Listeners []*Listener
	Port      v1.PortNumber
}

func (vl *VirtualListener) name() string {
	pname := ""
	switch vl.ProtocolCategory {
	case protocols.ProtocolCategoryInsecure:
		pname = "http"
	case protocols.ProtocolCategorySecure:
		// when we will merge ProtocolCategoryTLS into ProtocolCategorySecure, then we should remove this case and just use "tls" for ProtocolCategorySecure
		pname = "https"
	case protocols.ProtocolCategoryTLS:
		pname = "tls"
	case protocols.ProcotolCategoryTCP:
		pname = "tcp"
	case protocols.ProcotolCategoryUDP:
		pname = "udp"
	}
	return pname + "_" + strconv.FormatInt(int64(vl.Port), 10)
}

func NewVirtualListener(protocolCategory protocols.ProtocolCategory, port v1.PortNumber) *VirtualListener {
	vl := &VirtualListener{
		ProtocolCategory: protocolCategory,
		Port:             port,
		Listeners:        make([]*Listener, 0),
	}
	vl.Name = vl.name()
	return vl
}

// DeepCopy creates a deep copy of the VirtualListener using JSON marshalling.
func (vl *VirtualListener) DeepCopy() *VirtualListener {
	if vl == nil {
		return nil
	}

	data, err := json.Marshal(vl)
	if err != nil {
		return nil
	}

	var vlcopy VirtualListener
	if err := json.Unmarshal(data, &vlcopy); err != nil {
		return nil
	}

	return &vlcopy
}

// Equal checks if two VirtualListeners are equal by comparing ProtocolCategory, Port,
// and each Listener's K8sResource.
func (vl *VirtualListener) Equal(other *VirtualListener) bool {
	if vl == nil || other == nil {
		return vl == other
	}

	// Compare ProtocolCategory and Port
	if vl.ProtocolCategory != other.ProtocolCategory || vl.Port != other.Port {
		return false
	}

	// Compare number of listeners
	if len(vl.Listeners) != len(other.Listeners) {
		return false
	}

	// Compare each Listener's K8sResource
	for i := range vl.Listeners {
		if !cmp.Equal(vl.Listeners[i].K8sResource, other.Listeners[i].K8sResource) {
			return false
		}
	}

	// if condition Programmed is Unknown,
	// we consider the VirtualListener as not equal
	//  to trigger a status update to Pending and avoid keeping a stale Programmed condition on the listeners of this VirtualListener
	for _, l := range vl.Listeners {
		progCond, exists := l.Conditions.GetCondition(generic.ConditionType(v1.ListenerConditionProgrammed))
		if !exists || progCond.Status == metav1.ConditionUnknown {
			return false
		}
	}

	return true
}

// SetAsUpserted marks the VirtualListener as upserted.
func (vl *VirtualListener) SetAsUpserted(logger *slog.Logger) {
	logMessage := fmt.Sprintf("%T %s", *vl, store.StatusUpserted)
	logger.LogAttrs(context.Background(), slog.LevelDebug, logMessage,
		slog.String("virtualListenerName", vl.Name))

	vl.Status = store.StatusUpserted
}

// SetAsDeleted marks the VirtualListener as deleted.
func (vl *VirtualListener) SetAsDeleted(logger *slog.Logger) {
	logMessage := fmt.Sprintf("%T %s", *vl, store.StatusDeleted)
	logger.LogAttrs(context.Background(), slog.LevelDebug, logMessage,
		slog.String("virtualListenerName", vl.Name))

	vl.Status = store.StatusDeleted
}

// SetAsCreated marks the VirtualListener as created.
func (vl *VirtualListener) SetAsCreated(logger *slog.Logger) {
	logMessage := fmt.Sprintf("%T %s", *vl, store.StatusUpserted)
	logger.LogAttrs(context.Background(), slog.LevelDebug, logMessage,
		slog.String("virtualListenerName", vl.Name))

	vl.Status = store.StatusUpserted
}
