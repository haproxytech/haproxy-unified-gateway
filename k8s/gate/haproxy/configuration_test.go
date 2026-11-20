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
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/require"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/diffs"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/structured"
)

func newTestConfiguration() Configuration {
	return Configuration{
		structured: structured.NewStructuredConf(),
		diffs: diffs.HaproxyConfDiffs{
			Created: structured.NewStructuredConf(),
			Updated: structured.NewStructuredConf(),
			Deleted: structured.NewStructuredConf(),
		},
	}
}

// A creation the API refused never reached the file, so the correction recorded
// for the same section cannot edit it: the newest content has to travel on the
// creation, or the replay fails on the refused content again and never applies the
// correction.
func TestReconcilePendingDiffsCarriesTheCorrectionOnAPendingCreation(t *testing.T) {
	c := newTestConfiguration()

	c.diffs.Created.Frontends["fe"] = &models.Frontend{Name: "fe", Mode: "invalid"}
	corrected := &models.Frontend{Name: "fe", Mode: "http"}
	c.structured.Frontends["fe"] = corrected
	c.diffs.Updated.Frontends["fe"] = corrected

	c.reconcilePendingDiffs()

	require.Equal(t, "http", c.diffs.Created.Frontends["fe"].Mode,
		"the replay would create the refused content before the correction")
	require.NotContains(t, c.diffs.Updated.Frontends, "fe",
		"an update cannot find the section its creation never added")
}

// A section deleted then re-created while the replay was pending is desired again:
// the deletion has to go, and the creation stays.
func TestReconcilePendingDiffsKeepsARecreatedSection(t *testing.T) {
	c := newTestConfiguration()

	recreated := &models.Frontend{Name: "fe", Mode: "http"}
	c.structured.Frontends["fe"] = recreated
	c.diffs.Created.Frontends["fe"] = recreated
	c.diffs.Deleted.Frontends["fe"] = &models.Frontend{Name: "fe"}

	c.reconcilePendingDiffs()

	require.NotContains(t, c.diffs.Deleted.Frontends, "fe")
	require.Contains(t, c.diffs.Created.Frontends, "fe")
}

// A section that is no longer desired loses its pending creation and its pending
// update: the deletion is what the cycle wants.
func TestReconcilePendingDiffsDropsTheCreationOfAnUndesiredSection(t *testing.T) {
	c := newTestConfiguration()

	deletedSection := &models.Frontend{Name: "fe", Mode: "http"}
	c.diffs.Created.Frontends["fe"] = deletedSection
	c.diffs.Updated.Frontends["fe"] = deletedSection
	c.diffs.Deleted.Frontends["fe"] = &models.Frontend{Name: "fe"}

	c.reconcilePendingDiffs()

	require.NotContains(t, c.diffs.Created.Frontends, "fe")
	require.NotContains(t, c.diffs.Updated.Frontends, "fe",
		"an update of a section that is gone cannot run")
	require.Contains(t, c.diffs.Deleted.Frontends, "fe")
}

// The same rules apply to the sections that have no name in their diff content.
func TestReconcilePendingDiffsAppliesToGlobalsAndDefaults(t *testing.T) {
	c := newTestConfiguration()

	c.diffs.Created.Globals[structured.GlobalKey] = &models.Global{Daemon: false}
	correctedGlobal := &models.Global{Daemon: true}
	c.structured.Globals[structured.GlobalKey] = correctedGlobal
	c.diffs.Updated.Globals[structured.GlobalKey] = correctedGlobal

	c.reconcilePendingDiffs()

	require.True(t, c.diffs.Created.Globals[structured.GlobalKey].Daemon)
	require.NotContains(t, c.diffs.Updated.Globals, structured.GlobalKey)
}
