// Copyright 2025 HAProxy Technologies LLC
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
	"context"
	"log/slog"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
)

func (c *clientNative) UseBackendDeleteAll(name string) error {
	configuration, err := c.nativeAPI.Configuration()
	if err != nil {
		return err
	}
	_, rules, errGet := configuration.GetBackendSwitchingRules(name, c.activeTransaction)
	if errGet != nil {
		return errGet
	}

	for index := len(rules) - 1; index >= 0; index-- {
		errDelete := configuration.DeleteBackendSwitchingRule(int64(index), name, c.activeTransaction, 0)
		if errDelete != nil {
			return errDelete
		}
	}
	return nil
}

func (c *clientNative) UseBackendCreate(index int, name string, rule *models.BackendSwitchingRule) error {
	configuration, err := c.nativeAPI.Configuration()
	if err != nil {
		return err
	}
	return configuration.CreateBackendSwitchingRule(int64(index), name, rule, c.activeTransaction, 0)
}

func (c *clientNative) UseBackendReplaceAll(name string, rules models.BackendSwitchingRules) error {
	err := c.UseBackendDeleteAll(name)
	if err != nil {
		return err
	}

	for index, rule := range rules {
		err := c.UseBackendCreate(index, name, rule)
		if err != nil {
			c.logger.LogAttrs(
				context.Background(), slog.LevelError, "failed to create use-backend rule",
				logging.LogAttrError(err),
				slog.Int("rule index", index),
				slog.String("frontend", name),
			)
			// Best effort
			continue
		}
	}

	return nil
}
