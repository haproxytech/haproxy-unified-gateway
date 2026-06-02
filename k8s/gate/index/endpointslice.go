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
package index

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	discoveryV1 "k8s.io/api/discovery/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// EndpointSliceServiceNameIndexField is the name of the Index Field used to index EndpointSlices by their service
	// owners.
	EndpointSliceServiceNameIndexField = "k8sServiceName"
	// EndpointSliceServiceNameLabel is the label used to identify the Kubernetes service name on an EndpointSlice.
	EndpointSliceServiceNameLabel = "kubernetes.io/service-name"
)

// CreateEndpointSliceFieldIndices creates a FieldIndices map for the EndpointSlice resource.
func CreateEndpointSliceFieldIndices(logger *slog.Logger) FieldIndices {
	return FieldIndices{
		EndpointSliceServiceNameIndexField: ServiceNameIndexFunc(logger),
	}
}

type ServiceNameIndexFuncWithLogger func(obj client.Object) []string

// ServiceNameIndexFunc is a client.IndexerFunc that parses a Kubernetes object and returns the value of the
// Kubernetes service-name label.
// Used to index EndpointSlices by their service owners.
func ServiceNameIndexFunc(logger *slog.Logger) client.IndexerFunc {
	return func(obj client.Object) []string {
		slice, ok := obj.(*discoveryV1.EndpointSlice)
		if !ok {
			logger.LogAttrs(
				context.Background(), slog.LevelError,
				fmt.Sprintf("expected an EndpointSlice; got %T", obj),
				logging.LogAttrCategory(logging.LogCategoryK8s),
			)
			return nil
		}

		if slice.Labels == nil {
			return nil
		}

		name := slice.Labels[EndpointSliceServiceNameLabel]
		if name == "" {
			return nil
		}

		return []string{name}
	}
}
