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
package storage

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/renameio"
	futils "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/fileutils"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/certificate"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/metrics"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type CertStorage interface {
	// CertPath returns the FilePath for the a Certificate
	CertPath(secretKey client.ObjectKey) futils.FilePath
	// NewCertificateData returns the new CertificateData for a given secret
	NewCertificateData(secret *v1.Secret) (certificate.CertificateData, error)
	WriteOnDisk(certData certificate.CertificateData) error
	DeleteFromDisk(certData certificate.CertificateData) error
	// DeleteEmptyCertsDir checks and deletes subdirectories directly
	// under the certs Base Dir (namespace level)
	DeleteEmptyCertsDir() error
}

var _ CertStorage = &CertificateStorageDefault{}

// CertificateStorageDefault handles a default storage for certificates
type CertificateStorageDefault struct {
	logger     *slog.Logger
	extractGVK utilsk8s.ExtractGVK
	// CertsBaseDir the base directory to store certificates
	// /usr/local/hug/certs/<namespace>/my/
	CertsBaseDir string
	// CertFilesBaseDir is the base directory where crt-list files are stored
	CertFilesBaseDir string
	LinkID           string
}

func NewCertificateStorage(logger *slog.Logger, extractGVK utilsk8s.ExtractGVK, structureType StructureType, linkID, certsBaseDir, certFileBaseDir string) (CertificateStorage, error) {
	mylogger := logger.With(logging.LogAttrCategory(logging.LogCategoryCertsStorage))

	switch structureType {
	case StructureTypeCertDefault:
		cs := CertificateStorageDefault{
			CertsBaseDir:     certsBaseDir,
			CertFilesBaseDir: certFileBaseDir,
			logger:           mylogger,
			extractGVK:       extractGVK,
			LinkID:           linkID,
		}
		cs.empty(certsBaseDir)
		cs.empty(certFileBaseDir)
		return &cs, nil
	default:
		return nil, fmt.Errorf("unknown structure type: %s", structureType)
	}
}

// CertPath returns the FilePath for the a Certificate
// Default algorithm for Certificate Storage
// - For directory: certificates are grouped in directories based on:
//   - namespace/[take two first characters of a secret name as folder] to avoid having too many of them in the same directory
//     For example for secrets: namespace/secret-name-1 , namespace/secret-name-2, namespace/my-secret-name-1
//     -/usr/local/hug/certs/<namespace>/se/
//     -/usr/local/hug/certs/<namespace>/se/
//     -/usr/local/hug/certs/<namespace>/my/
//
// For file name: secret name.pem
// -/usr/local/hug/certs/<namespace>/se/secret-name-1.pem
// -/usr/local/hug/certs/<namespace>/se/secret-name-2.pem
// -/usr/local/hug/certs/<namespace>/my/my-secret-name-1.pem
func (c *CertificateStorageDefault) CertPath(secretKey client.ObjectKey) futils.FilePath {
	return futils.FilePath{
		Dir:      filepath.Join(c.CertsBaseDir, secretKey.Namespace, secretKey.Name[:2]),
		FileName: fmt.Sprintf("%s_%s_%s.pem", c.LinkID, secretKey.Namespace, secretKey.Name),
	}
}

func (c *CertificateStorageDefault) NewCertificateData(secret *v1.Secret) (certificate.CertificateData, error) {
	var pemOk bool
	var certData certificate.CertificateData

	secretNsName := types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace}
	for _, k := range []string{"tls", "rsa", "ecdsa", "dsa"} {
		keyValue, keyOk := secret.Data[k+".key"]
		crtValue, crtOk := secret.Data[k+".crt"]
		if keyOk && crtOk {
			pemOk = true
			certFilePath := c.CertPath(secretNsName)
			certPath := certFilePath.FullPath()
			if k != "tls" {
				// HAProxy "cert bundle"
				certFilePath.FileName = fmt.Sprintf("%s.%s", certPath, k)
			}
			content := certContent(keyValue, crtValue)
			certData = certificate.NewCertificateData(certFilePath, content)
		}
	}
	if !pemOk {
		err := fmt.Errorf("certificate or private key missing in %s", secretNsName)
		c.logger.LogAttrs(
			context.Background(), slog.LevelError, "Certificate [new]",
			logging.LogAttrKey(secretNsName),
			logging.LogAttrError(err),
		)
		return certData, err
	}
	return certData, nil
}

func (c *CertificateStorageDefault) WriteOnDisk(certData certificate.CertificateData) error {
	start := time.Now()
	c.ensureDirectoryExists(certData.Path.Dir)
	certFullPath := certData.Path.FullPath()
	err := writeCert(certFullPath, certData.Data)
	metrics.CertStorageDuration.WithLabelValues("write").Observe(time.Since(start).Seconds())
	if err != nil {
		metrics.CertStorageOperations.WithLabelValues("write", "error").Inc()
		c.logger.LogAttrs(context.Background(), slog.LevelError, "Cert [not written] on disk",
			slog.String("cert", certFullPath),
			logging.LogAttrError(err))
		return err
	}

	metrics.CertStorageOperations.WithLabelValues("write", "ok").Inc()
	c.logger.LogAttrs(
		context.Background(), slog.LevelInfo, "Cert [written]",
		slog.String("cert", certFullPath),
	)
	return nil
}

func (c *CertificateStorageDefault) DeleteFromDisk(certData certificate.CertificateData) error {
	certFilePath := certData.Path

	defer deleteIfEmpty(certFilePath.Dir)

	fullPath := certFilePath.FullPath()

	err := certFilePath.DeleteFromDisk()
	if err == nil {
		metrics.CertStorageOperations.WithLabelValues("delete", "ok").Inc()
		c.logger.LogAttrs(
			context.Background(), slog.LevelInfo, "Cert [deleted]",
			slog.String("cert", fullPath),
		)
		return nil
	}
	if os.IsNotExist(err) {
		return nil
	}

	metrics.CertStorageOperations.WithLabelValues("delete", "error").Inc()
	c.logger.LogAttrs(context.Background(), slog.LevelError, "Cert [not deleted] from disk",
		slog.String("cert", fullPath),
		logging.LogAttrError(err))
	return err
}

// DeleteEmptyCertsDir checks and deletes subdirectories directly
// under the certs BaseDir path if they are empty
func (c *CertificateStorageDefault) DeleteEmptyCertsDir() error {
	entries, err := os.ReadDir(c.CertsBaseDir)
	if err != nil {
		return err
	}

	// Iterates over namespace directories
	for _, entry := range entries {
		if !entry.IsDir() {
			continue // We only care about directories
		}

		subdirPath := filepath.Join(c.CertsBaseDir, entry.Name())
		subEntries, err := os.ReadDir(subdirPath)
		if err != nil {
			return err
		}

		if len(subEntries) == 0 {
			c.logger.LogAttrs(context.Background(), slog.LevelDebug, "Deleting empty directory",
				slog.String("dir", subdirPath))
			err := os.RemoveAll(subdirPath)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *CertificateStorageDefault) ensureDirectoryExists(path string) {
	// Check if the directory exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Directory does not exist, so create it
		// os.MkdirAll creates the directory and any necessary parents
		err := os.MkdirAll(path, 0o755)
		if err != nil {
			c.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to create directory",
				logging.LogAttrError(err))
		}
	} else if err != nil {
		c.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to create directory",
			logging.LogAttrError(err))
	}
}

func (c *CertificateStorageDefault) empty(path string) {
	// First, remove the directory and all its contents
	err := os.RemoveAll(path)
	if err != nil {
		c.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to empty directory",
			logging.LogAttrError(err))
		return
	}

	// Then, recreate the empty directory with the specified permissions
	// Using 0755 for read/write/execute for owner, read/execute for group/others
	err = os.MkdirAll(path, 0o755)
	if err != nil {
		c.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to create directory",
			logging.LogAttrError(err))
	}

	c.logger.LogAttrs(context.Background(), slog.LevelDebug, "certs storage has been emptied",
		slog.String("dir", path))
}

func certContent(key, crt []byte) []byte {
	buff := make([]byte, 0, len(key)+len(crt)+1)
	buff = append(buff, key...)
	if len(key) > 0 && key[len(key)-1] != byte('\n') {
		buff = append(buff, byte('\n'))
	}
	buff = append(buff, crt...)
	return buff
}

func writeCert(filename string, content []byte) error {
	err := renameio.WriteFile(filename, content, 0o666)
	if err != nil {
		return err
	}
	return nil
}

func deleteIfEmpty(dirPath string) {
	// Read the directory contents
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return
	}

	// Check if the directory is empty
	if len(entries) == 0 {
		_ = os.Remove(dirPath)
	}
}
