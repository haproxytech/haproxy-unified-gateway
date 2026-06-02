// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package utilsk8s

import (
	"context"
	"fmt"
	"log/slog"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// ExtractGVK is a function that extracts the GroupVersionKind (GVK) of a client.object.
// It will log an error if the GKV cannot be extracted.
type ExtractGVK func(object client.Object) schema.GroupVersionKind

// NewExtractGKV creates a new MustExtractGVK function using the scheme.
func NewExtractGKV(scheme *runtime.Scheme, logger *slog.Logger) ExtractGVK {
	return func(obj client.Object) schema.GroupVersionKind {
		gvk, err := apiutil.GVKForObject(obj, scheme)
		if err != nil {
			// this should not happen
			logger.LogAttrs(
				context.Background(), slog.LevelError,
				fmt.Sprintf("could not extract GVK for object: %T", obj),
			)
		}

		return gvk
	}
}
