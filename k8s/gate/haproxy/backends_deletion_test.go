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
package haproxy

import (
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/metadata"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/structured"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// Exercise the real route-processing path with the other routes unchanged:
// they must not need another upsert to keep their shared backend alive.
func TestSharedBackendRouteRemoval(t *testing.T) {
	tests := []struct {
		name      string
		ownerType BackendOwnerType
		remaining int
		invalid   bool
	}{
		{"HTTPRoute/deleted/last-owner", BackendOwnerTypeHTTPRoute, 0, false},
		{"HTTPRoute/deleted/one-survivor", BackendOwnerTypeHTTPRoute, 1, false},
		{"HTTPRoute/deleted/shared", BackendOwnerTypeHTTPRoute, 2, false},
		{"HTTPRoute/invalid/last-owner", BackendOwnerTypeHTTPRoute, 0, true},
		{"HTTPRoute/invalid/one-survivor", BackendOwnerTypeHTTPRoute, 1, true},
		{"HTTPRoute/invalid/shared", BackendOwnerTypeHTTPRoute, 2, true},
		{"TLSRoute/deleted/last-owner", BackendOwnerTypeTLSRoute, 0, false},
		{"TLSRoute/deleted/one-survivor", BackendOwnerTypeTLSRoute, 1, false},
		{"TLSRoute/deleted/shared", BackendOwnerTypeTLSRoute, 2, false},
		{"TLSRoute/invalid/last-owner", BackendOwnerTypeTLSRoute, 0, true},
		{"TLSRoute/invalid/one-survivor", BackendOwnerTypeTLSRoute, 1, true},
		{"TLSRoute/invalid/shared", BackendOwnerTypeTLSRoute, 2, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const backendName = "hug_services_shared-auth_4200"
			removed := k8stypes.NamespacedName{Namespace: "team-a", Name: "removed-route"}
			cs := &tree.ControllerStore{GateTree: &tree.GateTree{
				HTTPRoutes: make(map[k8stypes.NamespacedName]*tree.HTTPRoute),
				TLSRoutes:  make(map[k8stypes.NamespacedName]*tree.TLSRoute),
			}}
			mgr, ok := NewHaproxyConfMgr(slog.New(slog.NewTextHandler(io.Discard, nil)), cs,
				structured.NewStructuredConf(), HaproxyConfMgrParams{}, nil, nil).(*HaproxyConfMgrImpl)
			if !ok {
				t.Fatal("unexpected configuration manager type")
			}
			mgr.firstSync.flag = false
			mgr.configuration.resetDiffs()
			mgr.backendOwners.add(backendName, tc.ownerType, removed, 1)
			expectedInfo := make(map[string]metadata.RouteMetadaInfo)
			for _, routeName := range []string{"surviving-route-a", "surviving-route-b"}[:tc.remaining] {
				key := k8stypes.NamespacedName{Namespace: "team-a", Name: routeName}
				mgr.backendOwners.add(backendName, tc.ownerType, key, 2)
				expectedInfo[key.String()] = metadata.RouteMetadaInfo{OwnerType: string(tc.ownerType), Generation: 2}
				if tc.ownerType == BackendOwnerTypeHTTPRoute {
					cs.GateTree.HTTPRoutes[key] = &tree.HTTPRoute{Valid: true, TreeStatus: tree.TreeUpdate[tree.HTTPRoute]{Status: store.StatusUnchanged}}
				} else {
					cs.GateTree.TLSRoutes[key] = &tree.TLSRoute{Valid: true, TreeStatus: tree.TreeUpdate[tree.TLSRoute]{Status: store.StatusUnchanged}}
				}
			}
			backend := &models.Backend{}
			backend.Name = backendName
			backend.Servers = map[string]models.Server{"auth-1": {Address: "192.0.2.10"}}
			mgr.configuration.structured.Backends[backendName] = backend
			status := store.StatusDeleted
			if tc.invalid {
				status = store.StatusUpserted
			}
			var err error
			if tc.ownerType == BackendOwnerTypeHTTPRoute {
				cs.GateTree.HTTPRoutes[removed] = &tree.HTTPRoute{TreeStatus: tree.TreeUpdate[tree.HTTPRoute]{Status: status}}
				err = mgr.processHTTPRoutes()
			} else {
				cs.GateTree.TLSRoutes[removed] = &tree.TLSRoute{TreeStatus: tree.TreeUpdate[tree.TLSRoute]{Status: status}}
				err = mgr.processTLSRoutes()
			}
			if err != nil {
				t.Fatal(err)
			}
			_, exists := mgr.configuration.structured.Backends[backendName]
			_, deleted := mgr.GetDiffs().Deleted.Backends[backendName]
			if tc.remaining == 0 {
				if exists || !deleted {
					t.Fatal("last owner removed, but backend was not deleted")
				}
				return
			}
			if !exists || deleted {
				t.Fatal("shared backend deleted while unchanged routes still reference it")
			}
			updated := mgr.GetDiffs().Updated.Backends[backendName]
			if updated == nil {
				t.Fatal("surviving backend ownership was not updated")
			}
			if !reflect.DeepEqual(updated.Servers, backend.Servers) {
				t.Fatal("updating ownership must preserve backend servers")
			}
			want := mgr.metadataManager.BackendMetaData(expectedInfo)
			if !reflect.DeepEqual(backend.Metadata, map[string]any(want)) {
				t.Fatalf("backend metadata must contain only surviving routes: got %#v, want %#v", backend.Metadata, want)
			}
		})
	}
}
