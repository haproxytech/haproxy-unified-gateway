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

type DedicatedGateway struct {
	nsName types.NamespacedName
}

func NewDedicatedGateway(nsName types.NamespacedName) DedicatedGateway {
	return DedicatedGateway{nsName: nsName}
}

// Check reports whether gatewayNsName matches the dedicated Gateway this instance was configured
// with.
// If no dedicated Gateway was configured (both name and namespace are empty), it returns
// true, allowing all gateways through.
func (dg DedicatedGateway) Check(gatewayNsName types.NamespacedName) bool {
	// No dedicated Gateway configured, all events should be allowed
	if dg.nsName.Name == "" && dg.nsName.Namespace == "" {
		return true
	}
	return dg.nsName.Name == gatewayNsName.Name && dg.nsName.Namespace == gatewayNsName.Namespace
}
