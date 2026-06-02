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
	"fmt"
	"log/slog"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/constants"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type SupportedVersions []string

var (
	SupportedGatewayAPIBundleVersion = SupportedVersions{"v1.3", "v1.4", "v1.5"}
	SupportedParametersRefKind       = gatewayv1.Kind("HugGate")
	SupportedParametersRefGroup      = gatewayv1.Group("gate.v3.haproxy.org")
)

type InstalledVersions struct {
	// Versions contains the count of installed Gateway API versions.
	Versions map[string]int // map GwApi CRD version -> counter
	Updated  *bool
	Valid    bool
}

func (s SupportedVersions) String() string {
	return strings.Join(s, ", ")
}

type InstalledVersionsBuilderImpl struct {
	*ControllerStore
}

func NewInstalledVersionsBuilder(params *ControllerStore) *InstalledVersionsBuilderImpl {
	return &InstalledVersionsBuilderImpl{
		ControllerStore: params,
	}
}

func (b *InstalledVersionsBuilderImpl) Build() {
	for nsname, update := range b.ClusterStore.Updates.GatewayAPICRDs {
		switch update.Status {
		case store.StatusUpserted:
			b.buildUpserted(nsname, update)
		case store.StatusDeleted:
			b.buildDeleted(update.OldObject)
		}
		b.Logger.LogAttrs(
			context.Background(), slog.LevelDebug,
			"Installed versions",
			logging.LogAttrInstalledVersions(b.InstalledGwAPIVersions.Versions),
		)
	}
	if len(b.ClusterStore.Updates.GatewayAPICRDs) != 0 {
		b.onUpdateInstalledVersion()
	}
}

func (b *InstalledVersionsBuilderImpl) buildUpserted(nsname types.NamespacedName, update store.Update[*metav1.PartialObjectMetadata]) {
	gwapiCRD, ok := b.ClusterStore.GatewayAPICRDs[nsname]
	if !ok {
		err := fmt.Errorf("gwapi CRD not found for %s", nsname)
		b.Logger.LogAttrs(
			context.Background(), slog.LevelDebug,
			"gwapi CRD not found",
			logging.LogAttrError(err),
		)
		return
	}
	bundleVersion := gwapiCRD.Annotations[constants.BundleVersionAnnotation]

	if update.OldObject != nil {
		previousBundleVersion := update.OldObject.Annotations[constants.BundleVersionAnnotation]
		if previousBundleVersion == bundleVersion {
			return
		}
		b.InstalledGwAPIVersions.Versions[previousBundleVersion]--
		if b.InstalledGwAPIVersions.Versions[previousBundleVersion] == 0 {
			delete(b.InstalledGwAPIVersions.Versions, previousBundleVersion)
		}
	}
	b.InstalledGwAPIVersions.Versions[bundleVersion]++
}

func (b *InstalledVersionsBuilderImpl) buildDeleted(previous *metav1.PartialObjectMetadata) {
	bundleVersion := previous.Annotations[constants.BundleVersionAnnotation]
	b.InstalledGwAPIVersions.Versions[bundleVersion]--
	if b.InstalledGwAPIVersions.Versions[bundleVersion] == 0 {
		delete(b.InstalledGwAPIVersions.Versions, bundleVersion)
	}
}

// onUpdateInstalledVersion callback function to be called when the installed versions are updated.
func (b *InstalledVersionsBuilderImpl) onUpdateInstalledVersion() {
	b.Logger.LogAttrs(
		context.Background(), slog.LevelDebug,
		"OnUpdateInstalledVersion",
		logging.LogAttrInstalledVersions(b.InstalledGwAPIVersions.Versions),
	)
	// Retrieve Gateway API bundle version
	// using the BundleVersionAnnotation annotation present in all Gateway API CRDs.
	validateVersionsParams := validateVersionsParams{
		supportedVersions:      SupportedGatewayAPIBundleVersion,
		installedGwAPIVersions: b.InstalledGwAPIVersions.Versions,
	}
	versionValidUpdated := b.checkSupportedVersion(validateVersionsParams)
	if b.InstalledGwAPIVersions.Updated == nil {
		updated := true
		b.InstalledGwAPIVersions.Updated = &updated
		return
	}
	b.InstalledGwAPIVersions.Updated = &versionValidUpdated
}

type validateVersionsParams struct {
	installedGwAPIVersions map[string]int
	supportedVersions      []string
}

func (b *InstalledVersionsBuilderImpl) checkSupportedVersion(params validateVersionsParams) bool {
	oldValue := b.InstalledGwAPIVersions.Valid
	newValue := true
	for v := range params.installedGwAPIVersions {
		params := validateOneGwAPIVersionParams{
			supportedVersions: params.supportedVersions,
			installedVersion:  v,
		}
		newValue = newValue && b.validateOneInstalledGwAPIVersion(params)
	}
	b.InstalledGwAPIVersions.Valid = newValue
	return newValue != oldValue
}

type validateOneGwAPIVersionParams struct {
	installedVersion  string
	supportedVersions []string
}

func (b *InstalledVersionsBuilderImpl) validateOneInstalledGwAPIVersion(params validateOneGwAPIVersionParams) bool {
	constraints := make([]*semver.Constraints, 0)

	for _, v := range params.supportedVersions {
		constraint, err := semver.NewConstraint("~" + v)
		if err != nil {
			b.Logger.LogAttrs(
				context.Background(), slog.LevelError,
				"cannot build semver constraint",
				logging.LogAttrError(err),
			)
			return false
		}
		constraints = append(constraints, constraint)
	}

	sv, err := semver.NewVersion(params.installedVersion)
	if err != nil {
		// If a version string is invalid, we should not consider it as a supported version.
		b.Logger.LogAttrs(
			context.Background(), slog.LevelError,
			"cannot parse version string",
			logging.LogAttrError(err),
		)
		return false
	}
	for _, constraint := range constraints {
		if constraint.Check(sv) {
			return true
		}
	}

	return false
}
