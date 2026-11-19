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
package api

import (
	"errors"
	"fmt"
	"testing"

	"github.com/haproxytech/client-native/v6/configuration"
	"github.com/stretchr/testify/require"
)

// TestIsSectionNotFound checks the two refusals a replayed deletion has to read
// as success: the section is already gone, or its whole parent is. Anything else
// is a real error and must stay one.
func TestIsSectionNotFound(t *testing.T) {
	require.True(t, isSectionNotFound(configuration.ErrObjectDoesNotExist),
		"deleting a section that is already gone is the desired outcome")
	require.True(t, isSectionNotFound(configuration.ErrParentDoesNotExist),
		"a section whose parent is gone is gone too")
	require.True(t, isSectionNotFound(fmt.Errorf("delete frontend: %w", configuration.ErrObjectDoesNotExist)),
		"the error comes wrapped from client-native")
	require.False(t, isSectionNotFound(errors.New("transaction does not exist")),
		"any other failure is a real error")
	require.False(t, isSectionNotFound(nil), "no error is not a missing section")
}
