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

	parser "github.com/haproxytech/client-native/v6/config-parser"
	"github.com/haproxytech/client-native/v6/models"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
)

func (c *clientNative) ACLsDeleteAll(parentType parser.Section, name string) error {
	configuration, err := c.nativeAPI.Configuration()
	if err != nil {
		return err
	}
	_, rules, errGet := configuration.GetACLs(string(parentType), name, c.activeTransaction)
	if errGet != nil {
		return errGet
	}

	for index := len(rules) - 1; index >= 0; index-- {
		errDelete := configuration.DeleteACL(int64(index), string(parentType), name, c.activeTransaction, 0)
		if errDelete != nil {
			return errDelete
		}
	}
	return nil
}

func (c *clientNative) ACLsCreate(index int, parentType parser.Section, name string, rule *models.ACL) error {
	configuration, err := c.nativeAPI.Configuration()
	if err != nil {
		return err
	}
	return configuration.CreateACL(int64(index), string(parentType), name, rule, c.activeTransaction, 0)
}

func (c *clientNative) ACLReplaceAll(parentType parser.Section, name string, rules models.Acls) error {
	err := c.ACLsDeleteAll(parentType, name)
	if err != nil {
		return err
	}

	for index, rule := range rules {
		err := c.ACLsCreate(index, parentType, name, rule)
		if err != nil {
			c.logger.LogAttrs(
				context.Background(), slog.LevelError, "failed to create ACL rule",
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

func (c *clientNative) ACLsReplaceAll(parentType parser.Section, name string, rules models.Acls) error {
	err := c.ACLsDeleteAll(parentType, name)
	if err != nil {
		return err
	}

	for index, rule := range rules {
		err := c.ACLsCreate(index, parentType, name, rule)
		if err != nil {
			c.logger.LogAttrs(
				context.Background(), slog.LevelError, "failed to create ACL rule",
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
