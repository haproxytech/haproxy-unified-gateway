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
package constants

const (
	// BundleVersionAnnotation is the annotation on Gateway API CRDs that contains the installed version.
	// https://gateway-api.sigs.k8s.io/guides/api-design/?h=version#supported-api-versions
	BundleVersionAnnotation = "gateway.networking.k8s.io/bundle-version"

	// Stats Frontend name
	StatsFrontendName = "stats"
	// DefaultsSectionName
	DefaultsSectionName = "haproxytech"

	// Hug Service
	HugServiceLabelKey = "app.kubernetes.io/name"
	HugServiceLabelVal = "haproxy-unified-gateway"
)
