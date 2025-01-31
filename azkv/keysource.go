/*
Package azkv contains an implementation of the go.mozilla.org/sops/v3/keys.MasterKey interface that encrypts and decrypts the
data key using Azure Key Vault with the Azure Go SDK.
*/
package azkv //import "go.mozilla.org/sops/v3/azkv"

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.mozilla.org/sops/v3/logging"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	"github.com/sirupsen/logrus"
)

var log *logrus.Logger

func init() {
	log = logging.NewLogger("AZKV")
}

// MasterKey is a Azure Key Vault key used to encrypt and decrypt sops' data key.
type MasterKey struct {
	VaultURL string
	Name     string
	Version  string

	EncryptedKey string
	CreationDate time.Time
}

func newKeyVaultClient(vaultURL string) (*azkeys.Client, error) {
	var err error

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		log.WithError(err).Error("Failed to create Azure authorizer")
		return nil, err
	}

	client, err := azkeys.NewClient(vaultURL, cred, nil)
	if err != nil {
		log.WithError(err).Error("Failed to create Azure Key Vault client")
		return nil, err
	}

	return client, nil
}

// NewMasterKey creates a new MasterKey from an URL, key name and version, setting the creation date to the current date
func NewMasterKey(vaultURL string, keyName string, keyVersion string) *MasterKey {
	return &MasterKey{
		VaultURL:     vaultURL,
		Name:         keyName,
		Version:      keyVersion,
		CreationDate: time.Now().UTC(),
	}
}

// MasterKeysFromURLs takes a comma separated list of Azure Key Vault URLs and returns a slice of new MasterKeys for them
func MasterKeysFromURLs(urls string) ([]*MasterKey, error) {
	var keys []*MasterKey
	if urls == "" {
		return keys, nil
	}
	for _, s := range strings.Split(urls, ",") {
		k, err := NewMasterKeyFromURL(s)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, nil
}

// NewMasterKeyFromURL takes an Azure Key Vault key URL and returns a new MasterKey
// URL format is {vaultUrl}/keys/{key-name}/{key-version}
func NewMasterKeyFromURL(url string) (*MasterKey, error) {
	k := &MasterKey{}
	re := regexp.MustCompile("^(https://[^/]+)/keys/([^/]+)/([^/]+)$")
	parts := re.FindStringSubmatch(url)
	if parts == nil || len(parts) < 2 {
		return nil, fmt.Errorf("Could not parse valid key from %q", url)
	}

	k.VaultURL = parts[1]
	k.Name = parts[2]
	k.Version = parts[3]
	k.CreationDate = time.Now().UTC()
	return k, nil
}

// EncryptedDataKey returns the encrypted data key this master key holds
func (key *MasterKey) EncryptedDataKey() []byte {
	return []byte(key.EncryptedKey)
}

// SetEncryptedDataKey sets the encrypted data key for this master key
func (key *MasterKey) SetEncryptedDataKey(enc []byte) {
	key.EncryptedKey = string(enc)
}

// Encrypt takes a sops data key, encrypts it with Key Vault and stores the result in the EncryptedKey field
func (key *MasterKey) Encrypt(dataKey []byte) error {
	client, err := newKeyVaultClient(key.VaultURL)
	if err != nil {
		return err
	}

	alg := azkeys.JSONWebKeyEncryptionAlgorithmRSAOAEP256
	params := azkeys.KeyOperationsParameters{Value: dataKey, Algorithm: &alg}

	res, err := client.Encrypt(context.Background(), key.Name, key.Version, params, nil)
	if err != nil {
		log.WithError(err).WithFields(logrus.Fields{
			"key":     key.Name,
			"version": key.Version,
		}).Error("Encryption failed")
		return fmt.Errorf("Failed to encrypt data: %w", err)
	}

	key.EncryptedKey = string(res.Result)
	log.WithFields(logrus.Fields{
		"key":     key.Name,
		"version": key.Version,
	}).Info("Encryption succeeded")

	return nil
}

// EncryptIfNeeded encrypts the provided sops' data key and encrypts it if it hasn't been encrypted yet
func (key *MasterKey) EncryptIfNeeded(dataKey []byte) error {
	if len(key.EncryptedKey) == 0 {
		return key.Encrypt(dataKey)
	}
	return nil
}

// Decrypt decrypts the EncryptedKey field with Azure Key Vault and returns the result.
func (key *MasterKey) Decrypt() ([]byte, error) {
	client, err := newKeyVaultClient(key.VaultURL)
	if err != nil {
		return nil, err
	}

	alg := azkeys.JSONWebKeyEncryptionAlgorithmRSAOAEP256
	params := azkeys.KeyOperationsParameters{Value: []byte(key.EncryptedKey), Algorithm: &alg}

	res, err := client.Decrypt(context.TODO(), key.Name, key.Version, params, nil)
	if err != nil {
		log.WithError(err).WithFields(logrus.Fields{
			"key":     key.Name,
			"version": key.Version,
		}).Error("Decryption failed")
		return nil, fmt.Errorf("Error decrypting key: %w", err)
	}

	log.WithFields(logrus.Fields{
		"key":     key.Name,
		"version": key.Version,
	}).Info("Decryption succeeded")
	return res.Result, nil
}

// NeedsRotation returns whether the data key needs to be rotated or not.
func (key *MasterKey) NeedsRotation() bool {
	return time.Since(key.CreationDate) > (time.Hour * 24 * 30 * 6)
}

// ToString converts the key to a string representation
func (key *MasterKey) ToString() string {
	return fmt.Sprintf("%s/keys/%s/%s", key.VaultURL, key.Name, key.Version)
}

// ToMap converts the MasterKey to a map for serialization purposes
func (key MasterKey) ToMap() map[string]interface{} {
	out := make(map[string]interface{})
	out["vaultUrl"] = key.VaultURL
	out["key"] = key.Name
	out["version"] = key.Version
	out["created_at"] = key.CreationDate.UTC().Format(time.RFC3339)
	out["enc"] = key.EncryptedKey
	return out
}
