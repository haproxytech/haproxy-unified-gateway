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
	"io"
	"log/slog"
	"testing"

	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	manageControllerName = "gate.haproxy.org/hug"
	anotherController    = "example.com/another-controller"
)

// gatewayClassFixture is a controller store holding one GatewayClass, which a test can
// hand from one controller to the other.
type gatewayClassFixture struct {
	builder *GatewayClassBuilderImpl
	store   *ControllerStore
	key     types.NamespacedName
}

func newGatewayClassFixture(t *testing.T, controllerName string) *gatewayClassFixture {
	t.Helper()

	key := types.NamespacedName{Name: "haproxy"}
	controllerStore := &ControllerStore{
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		ControllerName: manageControllerName,
		ClusterStore: &store.ClusterStore{
			GatewayClasses: map[types.NamespacedName]*gatewayv1.GatewayClass{
				key: {
					ObjectMeta: metav1.ObjectMeta{Name: key.Name},
					Spec:       gatewayv1.GatewayClassSpec{ControllerName: gatewayv1.GatewayController(controllerName)},
				},
			},
			HugGates: map[types.NamespacedName]*v3.HugGate{},
			Updates:  store.NewClusterUpdates(),
		},
		GateTree: &GateTree{
			GatewayClasses: map[types.NamespacedName]*GatewayClass{},
		},
		UnmanagedGateTree: &GateTree{
			GatewayClasses: map[types.NamespacedName]*GatewayClass{},
		},
		InstalledGwAPIVersions: &InstalledVersions{Valid: true},
	}

	return &gatewayClassFixture{
		builder: &GatewayClassBuilderImpl{ControllerStore: controllerStore},
		store:   controllerStore,
		key:     key,
	}
}

// handOver repoints the class at another controller, the way another controller taking a
// Gateway over looks from here.
func (f *gatewayClassFixture) handOver(controllerName string) {
	f.store.ClusterStore.GatewayClasses[f.key].Spec.ControllerName = gatewayv1.GatewayController(controllerName)
	f.store.ClusterStore.Updates.GatewayClasses[f.key] = store.Update[*gatewayv1.GatewayClass]{
		Status:    store.StatusUpserted,
		NewObject: f.store.ClusterStore.GatewayClasses[f.key],
		OldObject: f.store.ClusterStore.GatewayClasses[f.key],
	}
	f.builder.ComputeTreeUpdates()
}

func (f *gatewayClassFixture) isManaged() bool {
	_, ok := f.store.GateTree.GatewayClasses[f.key]
	return ok
}

func (f *gatewayClassFixture) isUnmanaged() bool {
	_, ok := f.store.UnmanagedGateTree.GatewayClasses[f.key]
	return ok
}

// A GatewayClass handed to another controller has to leave the managed tree. It used to
// stay: Managed was set when the tree entry was built and never revisited, so the class
// kept answering "mine" for CheckGatewayClassExists and its Gateways went on being served
// here. The two directions were asymmetric, which is the tell -- the other way round
// worked, because that is when the entry is created.
func TestGatewayClassOwnershipFollowsItsControllerName(t *testing.T) {
	t.Run("a class of ours is managed", func(t *testing.T) {
		f := newGatewayClassFixture(t, manageControllerName)
		f.handOver(manageControllerName)

		assert.True(t, f.isManaged(), "a class naming this controller is served here")
		assert.False(t, f.isUnmanaged())
	})

	t.Run("a class handed to another controller leaves the managed tree", func(t *testing.T) {
		f := newGatewayClassFixture(t, manageControllerName)
		f.handOver(manageControllerName)
		require.True(t, f.isManaged())

		f.handOver(anotherController)

		assert.False(t, f.isManaged(),
			"the Gateways of this class must stop being served here")
		assert.True(t, f.isUnmanaged())
	})

	t.Run("a class handed to us enters the managed tree", func(t *testing.T) {
		f := newGatewayClassFixture(t, anotherController)
		f.handOver(anotherController)
		require.True(t, f.isUnmanaged())

		f.handOver(manageControllerName)

		assert.True(t, f.isManaged())
		assert.False(t, f.isUnmanaged())
	})
}
