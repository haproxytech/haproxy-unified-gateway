// Copyright 2026 HAProxy Technologies LLC
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
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// keyTheftRule is the rule the audit used to serve another tenant's key file.
func keyTheftRule() *models.HTTPRequestRule {
	return &models.HTTPRequestRule{
		Type:                "return",
		ReturnStatusCode:    new(int64(200)),
		ReturnContentFormat: "file",
		ReturnContent:       "/tmp/hug/certs/other/of/hug_other_offload.pem",
	}
}

func tenantCR() *v3.Backend {
	cr := &v3.Backend{}
	cr.Spec.ServerTimeout = new(int64(70000))
	cr.Spec.Balance = &models.Balance{Algorithm: new("leastconn")}
	cr.Spec.DefaultServer = &models.DefaultServer{
		Check:     "enabled",
		Inter:     new(int64(2000)),
		SslCafile: "/tmp/hug/certs/other/ca.pem",
	}
	cr.Spec.HTTPRequestRuleList = models.HTTPRequestRules{keyTheftRule()}
	cr.Spec.ErrorFiles = []*models.Errorfile{{Code: 503, File: "/etc/passwd"}}
	return cr
}

func TestRestrictBackendCR(t *testing.T) {
	t.Run("keeps tuning fields", func(t *testing.T) {
		got, _, err := restrictBackendCR(&tenantCR().Spec.Backend)
		require.NoError(t, err)
		assert.Equal(t, int64(70000), *got.ServerTimeout)
		assert.Equal(t, "leastconn", *got.Balance.Algorithm)
		assert.Equal(t, "enabled", got.DefaultServer.Check)
		assert.Equal(t, int64(2000), *got.DefaultServer.Inter)
	})

	t.Run("drops rule lists and file directives", func(t *testing.T) {
		got, dropped, err := restrictBackendCR(&tenantCR().Spec.Backend)
		require.NoError(t, err)
		assert.Empty(t, got.HTTPRequestRuleList)
		assert.Empty(t, got.ErrorFiles)
		assert.Empty(t, got.DefaultServer.SslCafile)
		assert.Equal(t, []string{"default_server.ssl_cafile", "error_files", "http_request_rule_list"}, dropped)
	})

	t.Run("does not modify the stored CR", func(t *testing.T) {
		cr := tenantCR()
		_, _, err := restrictBackendCR(&cr.Spec.Backend)
		require.NoError(t, err)
		assert.Len(t, cr.Spec.HTTPRequestRuleList, 1)
	})

	t.Run("rejects a line break in a kept string", func(t *testing.T) {
		cr := &v3.Backend{}
		cr.Spec.Balance = &models.Balance{Algorithm: new("hdr"), HdrName: "x)\n  http-request deny"}
		_, _, err := restrictBackendCR(&cr.Spec.Backend)
		assert.Error(t, err)
	})

	t.Run("rejects a value outside the model enum", func(t *testing.T) {
		cr := &v3.Backend{}
		cr.Spec.Balance = &models.Balance{Algorithm: new("roundrobin hash-type")}
		_, _, err := restrictBackendCR(&cr.Spec.Backend)
		assert.Error(t, err)
	})

	t.Run("keeps the space-separated retry conditions", func(t *testing.T) {
		cr := &v3.Backend{}
		cr.Spec.RetryOn = "conn-failure 503"
		got, _, err := restrictBackendCR(&cr.Spec.Backend)
		require.NoError(t, err)
		assert.Equal(t, "conn-failure 503", got.RetryOn)
	})
}

// newTrustTestMgr builds a manager that trusts no namespace and recognises a
// Backend ExtensionRef.
func newTrustTestMgr(backendCRs map[k8stypes.NamespacedName]*v3.Backend) *HaproxyConfMgrImpl {
	mgr := serviceCRMgr(nil, backendCRs)
	mgr.controllerStore.TrustedBackendCRNamespaces = nil
	mgr.params.extractGVK = func(client.Object) schema.GroupVersionKind {
		return schema.GroupVersionKind{Group: v3.GroupName, Version: "v3", Kind: "Backend"}
	}
	return mgr
}

// crBackendRef builds an HTTPBackendRef whose ExtensionRef names a Backend CR.
func crBackendRef(name string) gatewayv1.HTTPBackendRef {
	return gatewayv1.HTTPBackendRef{
		Filters: []gatewayv1.HTTPRouteFilter{{
			Type: gatewayv1.HTTPRouteFilterExtensionRef,
			ExtensionRef: &gatewayv1.LocalObjectReference{
				Group: gatewayv1.Group(v3.GroupName),
				Kind:  "Backend",
				Name:  gatewayv1.ObjectName(name),
			},
		}},
	}
}

func TestMergeWithBackendCRsTrust(t *testing.T) {
	crNN := k8stypes.NamespacedName{Namespace: "team-a", Name: "tuning"}

	t.Run("untrusted namespace gets tuning only", func(t *testing.T) {
		mgr := newTrustTestMgr(map[k8stypes.NamespacedName]*v3.Backend{crNN: tenantCR()})
		be := &models.Backend{}
		errs := mgr.mergeWithBackendCRs(crBackendRef("tuning"), be, "team-a")
		require.NoError(t, errs.Result())
		assert.Equal(t, int64(70000), *be.ServerTimeout)
		assert.Empty(t, be.HTTPRequestRuleList)
		assert.Empty(t, be.ErrorFiles)
	})

	t.Run("trusted namespace gets the full CR", func(t *testing.T) {
		mgr := newTrustTestMgr(map[k8stypes.NamespacedName]*v3.Backend{crNN: tenantCR()})
		mgr.controllerStore.TrustedBackendCRNamespaces = []string{"team-a"}
		be := &models.Backend{}
		errs := mgr.mergeWithBackendCRs(crBackendRef("tuning"), be, "team-a")
		require.NoError(t, errs.Result())
		assert.Len(t, be.HTTPRequestRuleList, 1)
		assert.Len(t, be.ErrorFiles, 1)
	})

	t.Run("namespace matching a trusted pattern gets the full CR", func(t *testing.T) {
		mgr := newTrustTestMgr(map[k8stypes.NamespacedName]*v3.Backend{crNN: tenantCR()})
		mgr.controllerStore.TrustedBackendCRNamespaces = []string{"team-*"}
		be := &models.Backend{}
		errs := mgr.mergeWithBackendCRs(crBackendRef("tuning"), be, "team-a")
		require.NoError(t, errs.Result())
		assert.Len(t, be.HTTPRequestRuleList, 1)
	})

	t.Run("unsafe CR from an untrusted namespace is not applied", func(t *testing.T) {
		cr := tenantCR()
		cr.Spec.Balance.HdrName = "x)\n  http-request deny"
		mgr := newTrustTestMgr(map[k8stypes.NamespacedName]*v3.Backend{crNN: cr})
		be := &models.Backend{}
		_ = mgr.mergeWithBackendCRs(crBackendRef("tuning"), be, "team-a")
		assert.Nil(t, be.ServerTimeout)
		assert.Nil(t, be.Balance)
	})
}

func TestMergeServiceBackendCRTrust(t *testing.T) {
	svcNN := k8stypes.NamespacedName{Namespace: "team-a", Name: "echo"}
	crNN := k8stypes.NamespacedName{Namespace: "team-a", Name: "tuning"}
	newMgr := func() *HaproxyConfMgrImpl {
		return serviceCRMgr(
			map[k8stypes.NamespacedName]*apiv1.Service{svcNN: annotatedService("team-a", "echo", "tuning")},
			map[k8stypes.NamespacedName]*v3.Backend{crNN: tenantCR()},
		)
	}

	t.Run("untrusted namespace gets tuning only", func(t *testing.T) {
		mgr := newMgr()
		mgr.controllerStore.TrustedBackendCRNamespaces = nil
		be := &models.Backend{}
		require.NoError(t, mgr.mergeServiceBackendCR(be, serviceHTTPBackendRef("echo"), "team-a"))
		assert.Equal(t, int64(70000), *be.ServerTimeout)
		assert.Empty(t, be.HTTPRequestRuleList)
	})

	t.Run("trusted namespace gets the full CR", func(t *testing.T) {
		mgr := newMgr()
		mgr.controllerStore.TrustedBackendCRNamespaces = []string{"team-a"}
		be := &models.Backend{}
		require.NoError(t, mgr.mergeServiceBackendCR(be, serviceHTTPBackendRef("echo"), "team-a"))
		assert.Len(t, be.HTTPRequestRuleList, 1)
	})
}
