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

//revive:disable-next-line:package-naming
package utils

import "k8s.io/apimachinery/pkg/types"

type DedicatedNamespaces struct {
	nsMap map[string]struct{}
}

func NewDedicatedNamespaces(namespaces []string) DedicatedNamespaces {
	nsMap := make(map[string]struct{})
	for _, ns := range namespaces {
		nsMap[ns] = struct{}{}
	}
	return DedicatedNamespaces{nsMap: nsMap}
}

func (dn DedicatedNamespaces) Check(gatewayNsName types.NamespacedName) bool {
	// No namespace filter configured, all events should be allowed
	if len(dn.nsMap) == 0 {
		return true
	}
	_, exists := dn.nsMap[gatewayNsName.Namespace]
	return exists
}
