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

	"github.com/haproxytech/client-native/v6/configuration"
)

// isSectionNotFound reports whether client-native refused an operation because
// the section does not exist (ErrObjectDoesNotExist), or because the section it
// would live in does not exist (ErrParentDoesNotExist).
//
// It exists so a deletion can be replayed. client-native rejects the deletion of
// a section that is already gone, which is what a replay of a cycle that was
// applied but not reloaded would do: the section is absent from the file, so the
// deletion fails for a technical reason on every retry and the replay never
// converges. A section that is already absent is the desired outcome, not an
// error.
func isSectionNotFound(err error) bool {
	return errors.Is(err, configuration.ErrObjectDoesNotExist) ||
		errors.Is(err, configuration.ErrParentDoesNotExist)
}
